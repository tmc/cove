package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSIPBootCommandsEnableMode verifies enable mode generates the right csrutil command.
func TestSIPBootCommandsEnableMode(t *testing.T) {
	cmds := generateSIPBootCommands("enable", "", "", false, true)

	if !hasCommand(cmds, BootCommand{Type: "type", Args: "csrutil enable"}) {
		t.Fatal("expected csrutil enable command")
	}
	if hasCommand(cmds, BootCommand{Type: "type", Args: "csrutil disable"}) {
		t.Fatal("should not contain csrutil disable")
	}
}

// TestSIPBootCommandsNoReboot verifies the reboot command is omitted when requested.
func TestSIPBootCommandsNoReboot(t *testing.T) {
	cmds := generateSIPBootCommands("disable", "", "", false, false)

	if hasCommand(cmds, BootCommand{Type: "type", Args: "reboot"}) {
		t.Fatal("should not have reboot when reboot=false")
	}
}

// TestSIPBootCommandsWithUsername verifies username authentication commands.
func TestSIPBootCommandsWithUsername(t *testing.T) {
	cmds := generateSIPBootCommands("disable", "admin", "secret", false, true)

	if !hasCommand(cmds, BootCommand{Type: "typeAndReturnIfText", Args: "Enter username|admin"}) &&
		!hasCommand(cmds, BootCommand{Type: "typeAndReturnIfText", Args: "user name|admin"}) {
		t.Fatal("expected username entry command")
	}
	if !hasCommand(cmds, BootCommand{Type: "typeAndReturnIfText", Args: "Enter password|secret"}) {
		t.Fatal("expected password entry command")
	}
}

// TestSIPBootCommandsStatusCheck verifies status check is included with auth credentials.
func TestSIPBootCommandsStatusCheck(t *testing.T) {
	cmds := generateSIPBootCommands("disable", "admin", "secret", false, true)

	if !hasCommand(cmds, BootCommand{Type: "type", Args: "csrutil status"}) {
		t.Fatal("expected csrutil status check after authenticated operation")
	}
}

// TestSIPBootCommandsNoStatusWithoutAuth verifies status check is skipped without auth.
func TestSIPBootCommandsNoStatusWithoutAuth(t *testing.T) {
	cmds := generateSIPBootCommands("disable", "", "", false, true)

	if hasCommand(cmds, BootCommand{Type: "type", Args: "csrutil status"}) {
		t.Fatal("should not include csrutil status without auth credentials")
	}
}

// TestSIPBootCommandsRecoveryFlow verifies the full recovery boot sequence.
func TestSIPBootCommandsRecoveryFlow(t *testing.T) {
	cmds := generateSIPBootCommands("disable", "", "secret", true, true)

	// Verify ordering: wait -> Options -> Continue -> Utilities|Terminal -> csrutil
	steps := []BootCommand{
		{Type: "waitForText", Args: "Options"},
		{Type: "click", Args: "Options"},
		{Type: "waitForText", Args: "Continue"},
		{Type: "click", Args: "Continue"},
		{Type: "waitForMenuText", Args: "Utilities"},
		{Type: "clickMenuItem", Args: "Utilities|Terminal"},
		{Type: "type", Args: "csrutil disable"},
	}

	lastIdx := -1
	for _, step := range steps {
		idx := indexOfCommand(cmds, step)
		if idx < 0 {
			t.Fatalf("missing command: %s %q", step.Type, step.Args)
		}
		if idx <= lastIdx {
			t.Fatalf("command %s %q (idx=%d) out of order (last=%d)", step.Type, step.Args, idx, lastIdx)
		}
		lastIdx = idx
	}
}

// TestWriteBootCommandsForSIP verifies round-trip: generate -> write -> parse.
func TestWriteBootCommandsForSIP(t *testing.T) {
	tmpDir := t.TempDir()
	cmds := generateSIPBootCommands("disable", "admin", "secret", true, true)

	path, err := writeBootCommandsForSIP(tmpDir, "disable", cmds)
	if err != nil {
		t.Fatalf("writeBootCommandsForSIP: %v", err)
	}

	if filepath.Base(path) != "sip-disable-commands.txt" {
		t.Errorf("unexpected filename: %s", filepath.Base(path))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read boot commands: %v", err)
	}

	content := string(data)
	t.Logf("boot commands file:\n%s", content)

	// Verify header comments.
	if !strings.Contains(content, "Auto-generated boot commands for SIP disable") {
		t.Error("missing header comment")
	}

	// Parse it back and verify round-trip.
	parsed, err := ParseBootCommands(content)
	if err != nil {
		t.Fatalf("ParseBootCommands: %v", err)
	}

	if len(parsed) != len(cmds) {
		t.Fatalf("parsed %d commands, want %d", len(parsed), len(cmds))
	}

	for i, want := range cmds {
		got := parsed[i]
		if got.Type != want.Type || got.Args != want.Args {
			t.Errorf("command[%d]: got {%s, %q}, want {%s, %q}", i, got.Type, got.Args, want.Type, want.Args)
		}
	}
}

// TestRecoveryDiskPath verifies the path construction.
func TestRecoveryDiskPath(t *testing.T) {
	path := RecoveryDiskPath("/home/user/.vz/vms/default")
	if path != "/home/user/.vz/vms/default/recovery-disk.img" {
		t.Errorf("RecoveryDiskPath = %q, want .../recovery-disk.img", path)
	}
}

// TestEnsureRecoveryDiskSkipsExisting verifies no-op when disk already exists.
func TestEnsureRecoveryDiskSkipsExisting(t *testing.T) {
	tmpDir := t.TempDir()
	diskPath := filepath.Join(tmpDir, "recovery-disk.img")

	// Create a fake existing disk.
	if err := os.WriteFile(diskPath, []byte("fake disk data"), 0644); err != nil {
		t.Fatal(err)
	}

	path, err := EnsureRecoveryDisk(tmpDir)
	if err != nil {
		t.Fatalf("EnsureRecoveryDisk: %v", err)
	}
	if path != diskPath {
		t.Errorf("path = %q, want %q", path, diskPath)
	}

	// Verify the file wasn't recreated (still our fake data).
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "fake disk data" {
		t.Error("EnsureRecoveryDisk recreated existing disk")
	}
}

// TestSIPBootCommandsFullPipeline verifies the complete generate -> write -> parse -> verify pipeline.
func TestSIPBootCommandsFullPipeline(t *testing.T) {
	tmpDir := t.TempDir()

	modes := []struct {
		name     string
		mode     string
		user     string
		pass     string
		confirm  bool
		reboot   bool
		wantCSR  string
	}{
		{"disable-noauth", "disable", "", "", false, true, "csrutil disable"},
		{"disable-auth", "disable", "admin", "secret", true, true, "csrutil disable"},
		{"enable-noauth", "enable", "", "", false, true, "csrutil enable"},
		{"enable-auth", "enable", "root", "toor", false, false, "csrutil enable"},
	}

	for _, tt := range modes {
		t.Run(tt.name, func(t *testing.T) {
			cmds := generateSIPBootCommands(tt.mode, tt.user, tt.pass, tt.confirm, tt.reboot)

			if !hasCommand(cmds, BootCommand{Type: "type", Args: tt.wantCSR}) {
				t.Fatalf("missing %s command", tt.wantCSR)
			}

			dir := filepath.Join(tmpDir, tt.name)
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}

			path, err := writeBootCommandsForSIP(dir, tt.mode, cmds)
			if err != nil {
				t.Fatalf("write: %v", err)
			}

			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			parsed, err := ParseBootCommands(string(data))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}

			if len(parsed) != len(cmds) {
				t.Errorf("round-trip: got %d commands, want %d", len(parsed), len(cmds))
			}
		})
	}
}
