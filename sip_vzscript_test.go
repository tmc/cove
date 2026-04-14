package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateSIPVZScript_DisableWithPasswordConfirmReboot(t *testing.T) {
	got := generateSIPVZScript("disable", "admin", "secret", true, true)

	wantSnippets := []string{
		`ocr-wait 'Options' 60s`,
		`startup-options`,
		`recovery-continue`,
		`key cmd+shift+t`,
		`type 'csrutil disable'`,
		`[text-visible:Are+you+sure] type-keycodes 'y'`,
		`[text-visible:%5By%2Fn%5D] type-keycodes 'y'`,
		`[text-visible:Authorized+user] type 'admin'`,
		`[text-visible:Authorized+user] wait-prompt-clear 'Authorized user'`,
		`[text-visible:Enter+username] wait-prompt-clear 'Enter username' 1s`,
		`[text-visible:Password] type-keycodes 'secret'`,
		`[text-visible:Enter+password] wait-prompt-clear 'Enter password' 1s`,
		`[text-visible:System+Integrity+Protection+is+off.] screenshot`,
		`[text-visible:System+Integrity+Protection+is+off.] type reboot`,
	}
	for _, want := range wantSnippets {
		if !strings.Contains(got, want) {
			t.Fatalf("generated script missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "<wait") {
		t.Fatalf("generated script should not use legacy angle-bracket DSL\n%s", got)
	}
	for _, old := range []string{"type-if-visible-return", "reboot-if-visible"} {
		if strings.Contains(got, old) {
			t.Fatalf("generated script should not use legacy guard command %q\n%s", old, got)
		}
	}
}

func TestGenerateSIPVZScript_NoReboot(t *testing.T) {
	got := generateSIPVZScript("disable", "", "", false, false)
	if strings.Contains(got, "type reboot") {
		t.Fatalf("unexpected reboot command in no-reboot script\n%s", got)
	}
}

func TestWriteVZScriptForSIP(t *testing.T) {
	tmpDir := t.TempDir()
	script := generateSIPVZScript("enable", "admin", "secret", false, true)

	path, err := writeVZScriptForSIP(tmpDir, "enable", script)
	if err != nil {
		t.Fatalf("writeVZScriptForSIP: %v", err)
	}
	if filepath.Base(path) != "sip-enable.vzscript" {
		t.Fatalf("filename = %q, want sip-enable.vzscript", filepath.Base(path))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	if string(data) != script {
		t.Fatalf("script round trip mismatch")
	}
}
