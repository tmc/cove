package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGuestPowerKeepAwakeScript(t *testing.T) {
	script := guestPowerKeepAwakeScript()
	for _, want := range []string{
		"pmset -a displaysleep 0 sleep 0 disksleep 0 disablesleep 1",
		"defaults write /Library/Preferences/com.apple.screensaver idleTime -int 0",
		"pmset -g custom",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
}

func TestGuestPowerAllowSleepScript(t *testing.T) {
	script := guestPowerAllowSleepScript(7)
	for _, want := range []string{
		"pmset -a displaysleep 7 sleep 7 disksleep 7",
		"defaults write /Library/Preferences/com.apple.screensaver idleTime -int 420",
		"pmset -g custom",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
}

func TestGuestPowerStatusScript(t *testing.T) {
	script := guestPowerStatusScript()
	for _, want := range []string{
		"pmset -g custom",
		"Screen saver idleTime",
		"defaults read /Library/Preferences/com.apple.screensaver idleTime",
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("script missing %q:\n%s", want, script)
		}
	}
}

func TestCtlPowerCommandErrorPaths(t *testing.T) {
	// Use a non-Linux VM dir (no linux-disk.img marker).
	sock := GetControlSocketPathForVM(t.TempDir())

	tests := []struct {
		name    string
		args    []string
		wantSub string
	}{
		{"no args", nil, "power requires action"},
		{"unknown action", []string{"bogus"}, "unknown power action"},
		{"non-numeric minutes", []string{"allow-sleep", "abc"}, "invalid sleep minutes"},
		{"zero minutes", []string{"allow-sleep", "0"}, "invalid sleep minutes"},
		{"negative minutes", []string{"allow-sleep", "-3"}, "invalid sleep minutes"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ctlPowerCommand(sock, tc.args, time.Second, false)
			if err == nil {
				t.Fatalf("ctlPowerCommand() error = nil, want %q", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("ctlPowerCommand() error = %v, want substring %q", err, tc.wantSub)
			}
		})
	}
}

func TestCtlPowerRefusesLinuxGuest(t *testing.T) {
	vmDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(vmDir, "linux-disk.img"), nil, 0644); err != nil {
		t.Fatalf("write linux marker: %v", err)
	}
	err := ctlPowerCommand(GetControlSocketPathForVM(vmDir), []string{"status"}, time.Second, false)
	if err == nil {
		t.Fatal("ctlPowerCommand() error = nil, want linux refusal")
	}
	if !strings.Contains(err.Error(), "power: not supported for Linux guests; use systemd-inhibit directly via agent-exec --daemon") {
		t.Fatalf("ctlPowerCommand() error = %v", err)
	}
}
