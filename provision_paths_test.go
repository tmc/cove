package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProvisionScriptGeneration verifies the generated provision script structure.
func TestProvisionScriptGeneration(t *testing.T) {
	tests := []struct {
		name   string
		config ProvisionConfig
		want   []string // substrings the script must contain
		reject []string // substrings the script must not contain
	}{
		{
			name: "basic admin",
			config: ProvisionConfig{
				Username: "testuser",
				Password: "secret123",
				Admin:    true,
			},
			want:   []string{"USERNAME='testuser'", "PASSWORD='secret123'", `ADMIN="true"`, "sysadminctl"},
			reject: []string{`BOOTSTRAP_RECOVERY="true"`},
		},
		{
			name: "non-admin",
			config: ProvisionConfig{
				Username: "limited",
				Password: "pass",
				Admin:    false,
			},
			want:   []string{"USERNAME='limited'", `ADMIN="false"`},
			reject: []string{`ADMIN="true"`},
		},
		{
			name: "bootstrap recovery",
			config: ProvisionConfig{
				Username:          "recuser",
				Password:          "recpass",
				Admin:             true,
				BootstrapRecovery: true,
			},
			want: []string{`BOOTSTRAP_RECOVERY="true"`, "_vzbootstrap", "-adminUser"},
		},
		{
			name: "special chars in password",
			config: ProvisionConfig{
				Username: "user",
				Password: "p@ss'w\"ord$!",
				Admin:    true,
			},
			want: []string{"PASSWORD="},
		},
		{
			name: "fullname override",
			config: ProvisionConfig{
				Username: "jdoe",
				Password: "pass",
				Fullname: "Jane Doe",
			},
			want: []string{"FULLNAME='Jane Doe'"},
		},
		{
			name: "enable sshd",
			config: ProvisionConfig{
				Username:   "sshuser",
				Password:   "pass",
				EnableSSHD: true,
			},
			want: []string{`ENABLE_SSHD="true"`, "setremotelogin on"},
		},
		{
			name: "install xcode cli",
			config: ProvisionConfig{
				Username:        "dev",
				Password:        "pass",
				InstallXcodeCLI: true,
			},
			want: []string{`INSTALL_XCODE_CLI="true"`, "Command Line Tools"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			script, err := generateEmbeddedProvisionScript(tt.config)
			if err != nil {
				t.Fatalf("generateEmbeddedProvisionScript: %v", err)
			}

			for _, s := range tt.want {
				if !strings.Contains(script, s) {
					t.Errorf("script missing %q", s)
				}
			}
			for _, s := range tt.reject {
				if strings.Contains(script, s) {
					t.Errorf("script should not contain %q", s)
				}
			}

			// All scripts should have self-cleanup.
			if !strings.Contains(script, "rm -f /Library/LaunchDaemons/com.github.tmc.vz-macos.provision.plist") {
				t.Error("script missing LaunchDaemon self-cleanup")
			}
			if !strings.Contains(script, "rm -f /var/db/vz-provision.sh") {
				t.Error("script missing script self-cleanup")
			}
		})
	}
}

// TestLaunchDaemonPlistGeneration verifies the LaunchDaemon plist structure.
func TestLaunchDaemonPlistGeneration(t *testing.T) {
	plist := generateEmbeddedLaunchDaemonPlist()

	required := []string{
		"com.github.tmc.vz-macos.provision",
		"/bin/bash",
		"/var/db/vz-provision.sh",
		"<key>RunAtLoad</key>",
		"<true/>",
		"<key>LaunchOnlyOnce</key>",
		"/var/log/vz-provision.log",
	}
	for _, s := range required {
		if !strings.Contains(plist, s) {
			t.Errorf("plist missing %q", s)
		}
	}
}

// TestShellEscape verifies shell escaping for embedded values.
func TestShellEscape(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "'simple'"},
		{"with space", "'with space'"},
		{"it's", "'it'\\''s'"},
		{"", "''"},
		{`"quoted"`, `'"quoted"'`},
		{"$var", "'$var'"},
	}
	for _, tt := range tests {
		got := shellEscape(tt.input)
		if got != tt.want {
			t.Errorf("shellEscape(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestProvisionManifestRoundTrip verifies manifest staging and recovery.
func TestProvisionManifestRoundTrip(t *testing.T) {
	stagingDir := t.TempDir()
	manifest := &ProvisionManifest{
		Version: 1,
		VMDir:   "/fake/vm/path",
	}

	// Stage some files.
	if err := stageFile(stagingDir, "private/var/db/test.sh", []byte("#!/bin/bash\necho ok"), 0755, "root:wheel", manifest); err != nil {
		t.Fatalf("stageFile: %v", err)
	}
	if err := stageFile(stagingDir, "Library/test.plist", []byte("<plist/>"), 0644, "", manifest); err != nil {
		t.Fatalf("stageFile: %v", err)
	}

	if len(manifest.Files) != 2 {
		t.Fatalf("manifest has %d files, want 2", len(manifest.Files))
	}

	// Write manifest.
	if err := writeManifest(stagingDir, manifest); err != nil {
		t.Fatalf("writeManifest: %v", err)
	}

	// Verify manifest file exists.
	data, err := os.ReadFile(filepath.Join(stagingDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest.json: %v", err)
	}

	var decoded ProvisionManifest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if decoded.Version != 1 {
		t.Errorf("version = %d, want 1", decoded.Version)
	}
	if len(decoded.Files) != 2 {
		t.Errorf("files = %d, want 2", len(decoded.Files))
	}

	// Read it back via the function.
	recovered, err := readManifest(stagingDir)
	if err != nil {
		t.Fatalf("readManifest: %v", err)
	}
	if recovered.VMDir != "/fake/vm/path" {
		t.Errorf("VMDir = %q, want /fake/vm/path", recovered.VMDir)
	}

	// Verify staged files exist on disk.
	for _, f := range recovered.Files {
		fp := filepath.Join(stagingDir, f.Path)
		if _, err := os.Stat(fp); err != nil {
			t.Errorf("staged file missing: %s", f.Path)
		}
	}
}

// TestStageLaunchDaemonProvisioning verifies the two-phase staging for LaunchDaemon files.
func TestStageLaunchDaemonProvisioning(t *testing.T) {
	stagingDir := t.TempDir()
	manifest := &ProvisionManifest{Version: 1}

	config := ProvisionConfig{
		Username: "stagetest",
		Password: "pass123",
		Admin:    true,
	}

	if err := stageLaunchDaemonProvisioning(stagingDir, config, manifest); err != nil {
		t.Fatalf("stageLaunchDaemonProvisioning: %v", err)
	}

	if len(manifest.Files) != 2 {
		t.Fatalf("manifest has %d files, want 2", len(manifest.Files))
	}

	// Verify script was staged.
	scriptPath := filepath.Join(stagingDir, "private", "var", "db", "vz-provision.sh")
	scriptData, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatalf("read staged script: %v", err)
	}
	if !strings.Contains(string(scriptData), "USERNAME='stagetest'") {
		t.Error("staged script missing username")
	}

	// Verify plist was staged.
	plistPath := filepath.Join(stagingDir, "Library", "LaunchDaemons", "com.github.tmc.vz-macos.provision.plist")
	plistData, err := os.ReadFile(plistPath)
	if err != nil {
		t.Fatalf("read staged plist: %v", err)
	}
	if !strings.Contains(string(plistData), "com.github.tmc.vz-macos.provision") {
		t.Error("staged plist missing label")
	}

	// Verify ownership metadata.
	for _, f := range manifest.Files {
		if f.Owner != "root:wheel" {
			t.Errorf("file %s: owner = %q, want root:wheel", f.Path, f.Owner)
		}
	}
}

func TestStageAutoLogin(t *testing.T) {
	stagingDir := t.TempDir()
	manifest := &ProvisionManifest{Version: 1}

	if err := stageAutoLogin(stagingDir, "stagetest", "pass123", manifest); err != nil {
		t.Fatalf("stageAutoLogin: %v", err)
	}
	if len(manifest.Files) != 4 {
		t.Fatalf("manifest has %d files, want 4", len(manifest.Files))
	}

	for _, rel := range []string{
		filepath.Join("private", "etc", "kcpassword"),
		filepath.Join("Library", "Preferences", "com.apple.loginwindow.plist"),
		filepath.FromSlash(autoLoginScriptRelativePath),
		filepath.FromSlash(autoLoginLaunchDaemonRelativePath),
	} {
		if _, err := os.Stat(filepath.Join(stagingDir, rel)); err != nil {
			t.Fatalf("staged file missing: %s: %v", rel, err)
		}
	}

	for _, f := range manifest.Files {
		if f.Owner != "root:wheel" {
			t.Errorf("file %s: owner = %q, want root:wheel", f.Path, f.Owner)
		}
	}
}

// TestProvisionPasswordRoundTrip verifies that password hashing produces verifiable results.
func TestProvisionPasswordRoundTrip(t *testing.T) {
	passwords := []string{"simple", "p@$$w0rd!", "with spaces", "unicöde"}
	for _, pw := range passwords {
		t.Run(pw, func(t *testing.T) {
			shd, err := GenerateShadowHashData(pw)
			if err != nil {
				t.Fatalf("GenerateShadowHashData: %v", err)
			}
			if !VerifyPassword(pw, shd) {
				t.Error("password verification failed")
			}
			if VerifyPassword(pw+"wrong", shd) {
				t.Error("wrong password should not verify")
			}
		})
	}
}

// TestProvisionUserPlistAdminVsNonAdmin verifies group membership differences.
func TestProvisionUserPlistAdminVsNonAdmin(t *testing.T) {
	admin, err := CreateUserPlist("admin1", "Admin One", "pass", 501, true)
	if err != nil {
		t.Fatal(err)
	}
	regular, err := CreateUserPlist("user1", "User One", "pass", 502, false)
	if err != nil {
		t.Fatal(err)
	}

	hasGroup := func(up *UserPlist, group string) bool {
		for _, g := range up.GroupMembership {
			if g == group {
				return true
			}
		}
		return false
	}

	if !hasGroup(admin, "admin") {
		t.Error("admin user missing admin group")
	}
	if !hasGroup(admin, "staff") {
		t.Error("admin user missing staff group")
	}
	if hasGroup(regular, "admin") {
		t.Error("regular user should not have admin group")
	}
	if !hasGroup(regular, "staff") {
		t.Error("regular user missing staff group")
	}
}

// TestProvisionKCPasswordRoundTrip verifies XOR encoding preserves password bytes.
func TestProvisionKCPasswordRoundTrip(t *testing.T) {
	key := []byte{0x7D, 0x89, 0x52, 0x23, 0xD2, 0xBC, 0xDD, 0xEA, 0xA3, 0xB9, 0x1F}

	passwords := []string{"test", "password123", "a", "hello world!"}
	for _, pw := range passwords {
		t.Run(pw, func(t *testing.T) {
			encoded := EncodeKCPassword(pw)

			// Decode and verify.
			decoded := make([]byte, len(pw)+1)
			for i := range decoded {
				decoded[i] = encoded[i] ^ key[i%len(key)]
			}
			got := string(decoded[:len(pw)])
			if got != pw {
				t.Errorf("round-trip mismatch: got %q, want %q", got, pw)
			}
			// Null terminator.
			if decoded[len(pw)] != 0 {
				t.Error("missing null terminator after password")
			}
		})
	}
}

// TestCreateHomeDirectoryStructure verifies directory creation.
func TestCreateHomeDirectoryStructure(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), "testuser")
	if err := CreateHomeDirectoryStructure(homeDir, 501, 20); err != nil {
		t.Fatalf("CreateHomeDirectoryStructure: %v", err)
	}

	expected := []string{
		"Desktop", "Documents", "Downloads",
		"Library", "Movies", "Music", "Pictures", "Public",
	}
	for _, d := range expected {
		if _, err := os.Stat(filepath.Join(homeDir, d)); err != nil {
			t.Errorf("missing directory: %s", d)
		}
	}

	// Verify .CFUserTextEncoding exists.
	if _, err := os.Stat(filepath.Join(homeDir, ".CFUserTextEncoding")); err != nil {
		t.Error("missing .CFUserTextEncoding")
	}
}
