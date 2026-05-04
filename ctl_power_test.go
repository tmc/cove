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

func TestCtlPowerRefusesLinuxGuest(t *testing.T) {
	vmDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(vmDir, "linux-disk.img"), nil, 0644); err != nil {
		t.Fatalf("write linux marker: %v", err)
	}
	err := ctlPowerCommand(GetControlSocketPathForVM(vmDir), []string{"status"}, time.Second, false)
	if err == nil {
		t.Fatal("ctlPowerCommand() error = nil, want linux refusal")
	}
	if !strings.Contains(err.Error(), "power: not supported for Linux guests") {
		t.Fatalf("ctlPowerCommand() error = %v", err)
	}
}
