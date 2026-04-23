package main

import (
	"strings"
	"testing"
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
