package main

import (
	"strings"
	"testing"
)

func TestAutoLoginRefreshCommand(t *testing.T) {
	cmd, err := autoLoginRefreshCommand("testuser", "secret123")
	if err != nil {
		t.Fatalf("autoLoginRefreshCommand: %v", err)
	}
	if strings.Contains(cmd, "secret123") {
		t.Fatal("refresh command leaked the raw password")
	}
	for _, want := range []string{
		"base64 -D > /etc/kcpassword",
		"base64 -D > /Library/Preferences/com.apple.loginwindow.plist",
		"chown root:wheel /etc/kcpassword",
		"chown root:wheel /Library/Preferences/com.apple.loginwindow.plist",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("refresh command missing %q", want)
		}
	}
}
