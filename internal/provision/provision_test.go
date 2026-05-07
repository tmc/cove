package provision

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateUsername(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid simple", "testuser", false},
		{"valid with underscore", "test_user", false},
		{"valid with numbers", "user123", false},
		{"empty", "", true},
		{"too long", string(make([]byte, 256)), true},
		{"reserved root", "root", true},
		{"reserved daemon", "daemon", true},
		{"reserved nobody", "nobody", true},
		{"reserved wheel", "wheel", true},
		{"reserved admin", "admin", true},
		{"reserved staff", "staff", true},
		{"reserved case insensitive", "Root", true},
		{"invalid slash", "user/name", true},
		{"invalid backslash", "user\\name", true},
		{"invalid colon", "user:name", true},
		{"invalid star", "user*name", true},
		{"invalid question", "user?name", true},
		{"invalid quote", "user\"name", true},
		{"invalid lt", "user<name", true},
		{"invalid gt", "user>name", true},
		{"invalid pipe", "user|name", true},
		{"invalid newline", "user\nname", true},
		{"invalid tab", "user\tname", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUsername(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateUsername(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestShellEscape(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "'simple'"},
		{"with space", "'with space'"},
		{"it's", "'it'\\''s'"},
		{"", "''"},
		{"\"quoted\"", "'\"quoted\"'"},
		{"$var", "'$var'"},
	}
	for _, tt := range tests {
		if got := ShellEscape(tt.input); got != tt.want {
			t.Errorf("ShellEscape(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestManifestHelpers(t *testing.T) {
	manifest := &ProvisionManifest{
		Version: 1,
		Files: []ProvisionManifestFile{
			{Path: filepath.Join("private", "var", "db", "vz-provision.sh"), Owner: "root:wheel"},
			{Path: filepath.Join("Library", "LaunchDaemons", "com.github.tmc.vz-macos.provision.plist"), Owner: "root:wheel"},
			{Path: filepath.Join("Library", "Preferences", "com.apple.loginwindow.plist"), Owner: "root:wheel"},
			{Path: filepath.Join("usr", "local", "bin", "vz-agent"), Owner: "root:wheel"},
			{Path: filepath.Join("private", "etc", "kcpassword")},
		},
	}

	if got := ManifestNeedsRootProvisioning(manifest, "private/var/db/vz-autologin.sh"); !got {
		t.Fatal("manifestNeedsRootProvisioning returned false")
	}
	if got := ManifestIncludesLoginScreenCredentials(manifest); !got {
		t.Fatal("ManifestIncludesLoginScreenCredentials returned false")
	}
	if got := ManifestIncludesAgent(manifest, "vz-agent", "com.github.tmc.vz-macos.vz-agent", "com.github.tmc.vz-macos.vz-agent-user"); !got {
		t.Fatal("ManifestIncludesAgent returned false")
	}

	want := []string{
		filepath.Join("/Volumes/Data", "private", "var", "db", "vz-provision.sh"),
		filepath.Join("/Volumes/Data", "Library", "LaunchDaemons", "com.github.tmc.vz-macos.provision.plist"),
		filepath.Join("/Volumes/Data", "Library", "Preferences", "com.apple.loginwindow.plist"),
		filepath.Join("/Volumes/Data", "usr", "local", "bin", "vz-agent"),
	}
	got := RootWheelVerifyTargets(manifest, "/Volumes/Data")
	if len(got) != len(want) {
		t.Fatalf("len(RootWheelVerifyTargets) = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("RootWheelVerifyTargets[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestStageAndReadManifest(t *testing.T) {
	stagingDir := t.TempDir()
	manifest := &ProvisionManifest{Version: 1, VMDir: "/fake/vm/path"}
	if err := StageFile(stagingDir, "private/var/db/test.sh", []byte("#!/bin/bash\necho ok"), 0755, "root:wheel", manifest); err != nil {
		t.Fatalf("StageFile: %v", err)
	}
	if err := StageFile(stagingDir, "Library/test.plist", []byte("<plist/>"), 0644, "", manifest); err != nil {
		t.Fatalf("StageFile: %v", err)
	}

	if err := WriteManifest(stagingDir, manifest); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	decoded := &ProvisionManifest{}
	data, err := os.ReadFile(filepath.Join(stagingDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if err := json.Unmarshal(data, decoded); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if decoded.Version != 1 {
		t.Fatalf("Version = %d, want 1", decoded.Version)
	}
	if len(decoded.Files) != 2 {
		t.Fatalf("len(Files) = %d, want 2", len(decoded.Files))
	}

	got, err := ReadManifest(stagingDir)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if got.VMDir != "/fake/vm/path" {
		t.Fatalf("VMDir = %q, want /fake/vm/path", got.VMDir)
	}
}

func TestStagingFingerprintRoundTrip(t *testing.T) {
	stagingDir := t.TempDir()
	opts := InjectOptions{
		Config: ProvisionConfig{
			Username:          "mlxqa",
			Password:          "pass",
			Admin:             true,
			BootstrapRecovery: true,
			EnableSSHD:        true,
		},
		SkipSetupAssistant: true,
		AutoLogin:          true,
		CreateUserPlist:    false,
		InjectAgent:        true,
		InjectGuestTools:   true,
		SSHKeyPath:         "/tmp/id_rsa.pub",
	}
	fp := MakeStagingFingerprint(opts)
	if err := WriteStagingFingerprint(stagingDir, fp); err != nil {
		t.Fatalf("WriteStagingFingerprint: %v", err)
	}
	got, ok := ReadStagingFingerprint(stagingDir)
	if !ok {
		t.Fatal("ReadStagingFingerprint not ok")
	}
	if got != fp {
		t.Fatalf("staging fingerprint mismatch: %#v != %#v", got, fp)
	}

	staging := &ProvisionManifest{Version: 1}
	if err := StageFile(stagingDir, "private/var/db/.touch", []byte("ok"), 0600, "", staging); err != nil {
		t.Fatalf("StageFile: %v", err)
	}
	if err := WriteManifest(stagingDir, staging); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	reusable, err := StagingMatchesOptions(stagingDir, opts)
	if err != nil {
		t.Fatalf("StagingMatchesOptions: %v", err)
	}
	if !reusable {
		t.Fatal("staging mismatch after write")
	}

	opts.Config.Username = "different"
	reusable, err = StagingMatchesOptions(stagingDir, opts)
	if err != nil {
		t.Fatalf("StagingMatchesOptions: %v", err)
	}
	if reusable {
		t.Fatal("staging unexpectedly reusable with different username")
	}
}
