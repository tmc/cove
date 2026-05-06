package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/vz-macos/proto/controlpb"
)

func TestApplyProvisioningPreWarmsBeforeDiskAttach(t *testing.T) {
	dir := t.TempDir()
	target := vmSelection{Directory: dir, Name: "prewarm-test"}
	if err := os.WriteFile(target.diskPath(), []byte("disk"), 0o644); err != nil {
		t.Fatalf("write disk: %v", err)
	}
	stagingDir := provisionStagingDirForVM(target)
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	if err := writeManifest(stagingDir, &ProvisionManifest{Version: 1}); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	oldPreWarm := preWarmAuthorizationHook
	oldAttach := attachAndMountDataVolumeHook
	defer func() {
		preWarmAuthorizationHook = oldPreWarm
		attachAndMountDataVolumeHook = oldAttach
	}()

	var order []string
	preWarmAuthorizationHook = func() error {
		order = append(order, "prewarm")
		return nil
	}
	attachAndMountDataVolumeHook = func(string) (string, string, string, error) {
		order = append(order, "attach")
		return "", "", "", os.ErrNotExist
	}

	err := applyProvisioningFilesForVM(target)
	if err == nil || !strings.Contains(err.Error(), "mount data volume") {
		t.Fatalf("applyProvisioningFilesForVM error = %v, want mount error", err)
	}
	if len(order) != 2 || order[0] != "prewarm" || order[1] != "attach" {
		t.Fatalf("order = %v, want [prewarm attach]", order)
	}
}

func TestApplyProvisioningDoesNotWarnWhenNativeAuthAvailable(t *testing.T) {
	dir := t.TempDir()
	target := vmSelection{Directory: dir, Name: "login-warning"}
	if err := os.WriteFile(target.diskPath(), []byte("disk"), 0o644); err != nil {
		t.Fatalf("write disk: %v", err)
	}
	stagingDir := provisionStagingDirForVM(target)
	if err := os.MkdirAll(filepath.Join(stagingDir, "Library", "LaunchDaemons"), 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	manifest := &ProvisionManifest{
		Version: 1,
		Files: []ProvisionManifestFile{
			{Path: filepath.Join("Library", "LaunchDaemons", "com.github.tmc.vz-macos.provision.plist"), Mode: "0644", Owner: "root:wheel"},
			{Path: filepath.Join("private", "etc", "kcpassword"), Mode: "0600", Owner: "root:wheel"},
		},
	}
	if err := writeManifest(stagingDir, manifest); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	oldEUID := provisionEffectiveUID
	oldWriter := provisionWarningWriter
	oldPreWarm := preWarmAuthorizationHook
	oldAttach := attachAndMountDataVolumeHook
	t.Cleanup(func() {
		provisionEffectiveUID = oldEUID
		provisionWarningWriter = oldWriter
		preWarmAuthorizationHook = oldPreWarm
		attachAndMountDataVolumeHook = oldAttach
	})

	var buf bytes.Buffer
	provisionEffectiveUID = func() int { return 501 }
	provisionWarningWriter = &buf
	preWarmAuthorizationHook = func() error { return nil }
	attachAndMountDataVolumeHook = func(string) (string, string, string, error) {
		return "", "", "", os.ErrNotExist
	}

	t.Setenv("COVE_FORCE_MANUAL_ELEVATION", "")
	err := applyProvisioningFilesForVM(target)
	if err == nil || !strings.Contains(err.Error(), "mount data volume") {
		t.Fatalf("applyProvisioningFilesForVM error = %v, want mount error", err)
	}
	if got := buf.String(); got != "" {
		t.Fatalf("warning = %q, want empty", got)
	}
}

func TestApplyProvisioningWarnsWhenManualElevationRequired(t *testing.T) {
	dir := t.TempDir()
	target := vmSelection{Directory: dir, Name: "login-warning"}
	if err := os.WriteFile(target.diskPath(), []byte("disk"), 0o644); err != nil {
		t.Fatalf("write disk: %v", err)
	}
	stagingDir := provisionStagingDirForVM(target)
	if err := os.MkdirAll(filepath.Join(stagingDir, "Library", "LaunchDaemons"), 0o755); err != nil {
		t.Fatalf("mkdir staging: %v", err)
	}
	manifest := &ProvisionManifest{
		Version: 1,
		Files: []ProvisionManifestFile{
			{Path: filepath.Join("Library", "LaunchDaemons", "com.github.tmc.vz-macos.provision.plist"), Mode: "0644", Owner: "root:wheel"},
			{Path: filepath.Join("private", "etc", "kcpassword"), Mode: "0600", Owner: "root:wheel"},
		},
	}
	if err := writeManifest(stagingDir, manifest); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := writeStagingFingerprint(stagingDir, stagingFingerprint{Username: "mlxqa", AutoLogin: true}); err != nil {
		t.Fatalf("write fingerprint: %v", err)
	}

	oldEUID := provisionEffectiveUID
	oldWriter := provisionWarningWriter
	oldPreWarm := preWarmAuthorizationHook
	oldAttach := attachAndMountDataVolumeHook
	t.Cleanup(func() {
		provisionEffectiveUID = oldEUID
		provisionWarningWriter = oldWriter
		preWarmAuthorizationHook = oldPreWarm
		attachAndMountDataVolumeHook = oldAttach
	})

	var buf bytes.Buffer
	provisionEffectiveUID = func() int { return 501 }
	provisionWarningWriter = &buf
	preWarmAuthorizationHook = func() error { return nil }
	attachAndMountDataVolumeHook = func(string) (string, string, string, error) {
		return "", "", "", os.ErrNotExist
	}

	t.Setenv("COVE_FORCE_MANUAL_ELEVATION", "1")
	err := applyProvisioningFilesForVM(target)
	if err == nil || !strings.Contains(err.Error(), "mount data volume") {
		t.Fatalf("applyProvisioningFilesForVM error = %v, want mount error", err)
	}
	got := buf.String()
	for _, want := range []string{
		"warning: macOS auto-login provisioning needs administrator privileges, but this environment cannot show the native admin dialog",
		"root:wheel",
		"cove -vm login-warning provision -user 'mlxqa' -password <password> -skip-setup-assistant -auto-login",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("warning missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "sudo cove") {
		t.Fatalf("warning suggests sudo:\n%s", got)
	}
}

func TestRootWheelVerifyTargetsIncludesEveryRootOwnedFile(t *testing.T) {
	manifest := &ProvisionManifest{
		Version: 1,
		Files: []ProvisionManifestFile{
			{Path: filepath.Join("private", "var", "db", "vz-provision.sh"), Owner: "root:wheel"},
			{Path: filepath.Join("Library", "LaunchDaemons", "com.github.tmc.vz-macos.provision.plist"), Owner: "root:wheel"},
			{Path: filepath.Join("private", "var", "db", ".AppleSetupDone")},
			{Path: filepath.Join("usr", "local", "bin", agentBinaryName), Owner: "root:wheel"},
		},
	}
	got := rootWheelVerifyTargets(manifest, "/Volumes/Data")
	want := []string{
		filepath.Join("/Volumes/Data", "private", "var", "db", "vz-provision.sh"),
		filepath.Join("/Volumes/Data", "Library", "LaunchDaemons", "com.github.tmc.vz-macos.provision.plist"),
		filepath.Join("/Volumes/Data", "usr", "local", "bin", agentBinaryName),
	}
	if len(got) != len(want) {
		t.Fatalf("len(rootWheelVerifyTargets) = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("rootWheelVerifyTargets[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

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
			err := validateUsername(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateUsername(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestLoginConsoleUserFromExec(t *testing.T) {
	tests := []struct {
		name    string
		result  *controlpb.AgentExecResponse
		want    string
		wantErr bool
	}{
		{name: "user", result: &controlpb.AgentExecResponse{Stdout: "overlaytest 502\n"}, want: "overlaytest"},
		{name: "root", result: &controlpb.AgentExecResponse{Stdout: "root 0\n"}, wantErr: true},
		{name: "exec failure stderr", result: &controlpb.AgentExecResponse{ExitCode: 1, Stderr: "stat failed\n"}, wantErr: true},
		{name: "nil", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := loginConsoleUserFromExec(tt.result)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("loginConsoleUserFromExec() got nil error")
				}
				return
			}
			if err != nil {
				t.Fatalf("loginConsoleUserFromExec(): %v", err)
			}
			if got != tt.want {
				t.Fatalf("loginConsoleUserFromExec() = %q, want %q", got, tt.want)
			}
		})
	}
}
