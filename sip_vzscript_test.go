package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateSIPVZScript_DisableWithPasswordConfirmReboot(t *testing.T) {
	got, err := generateSIPVZScript("disable", "admin", "secret", true, true)
	if err != nil {
		t.Fatal(err)
	}

	wantSnippets := []string{
		`recovery-options 180s`,
		`label-push 'Recovery picker'`,
		`recovery-continue 240s`,
		`wait-menu-text Utilities 180s`,
		`label-push 'Recovery Terminal'`,
		`key cmd+shift+t`,
		`ocr-wait '-bash-3.2#' 60s`,
		`key cmd+k`,
		`type-keycodes 'csrutil disable'`,
		`label-push 'csrutil prompts'`,
		`answer-visible -timeout 45s`,
		`'y/n' 'y'`,
		`'security level to full boot security' 'y'`,
		`answer-visible -optional -timeout 5s`,
		`'Authorized user' 'admin'`,
		`'user name' 'admin'`,
		`'Password' 'secret'`,
		`'password for user' 'secret'`,
		`ocr-wait 'System Integrity Protection is off.' 60s`,
		`[text-visible:System+Integrity+Protection+is+off.] screenshot`,
		`[text-visible:System+Integrity+Protection+is+off.] type-keycodes 'reboot'`,
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

func TestGenerateSIPVZScript_UsesCustomVZScriptCommandsAndConds(t *testing.T) {
	got, err := generateSIPVZScript("disable", "admin", "secret", true, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := executeVZScriptSyntaxOnly(t, "sip-disable.vzscript", []byte(got)); err != nil {
		t.Fatal(err)
	}

	engine := newVZScriptEngine(vzscriptConfig{})
	wantCmds := []string{
		"recovery-options",
		"recovery-continue",
		"wait-menu-text",
		"ocr-wait",
		"type-keycodes",
		"label-push",
		"label-pop",
		"answer-visible",
	}
	for _, name := range wantCmds {
		if _, ok := engine.Cmds[name]; !ok {
			t.Fatalf("missing vzscript command %q", name)
		}
		if !strings.Contains(got, name) {
			t.Fatalf("generated script does not use command %q\n%s", name, got)
		}
	}
	if _, ok := engine.Conds["text-visible"]; !ok {
		t.Fatal("missing text-visible condition")
	}
	if !strings.Contains(got, "[text-visible:") {
		t.Fatalf("generated script does not use text-visible condition\n%s", got)
	}
}

func TestGenerateSIPVZScript_NoReboot(t *testing.T) {
	got, err := generateSIPVZScript("disable", "", "", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "reboot") {
		t.Fatalf("unexpected reboot command in no-reboot script\n%s", got)
	}
}

func TestSIPTemplateCanRenderWithVZScriptTemplateVars(t *testing.T) {
	data, err := loadVZScriptData("sip-recovery")
	if err != nil {
		t.Fatal(err)
	}
	got, err := renderVZScriptTemplate(data, "sip-recovery", map[string]any{
		"Mode":     "disable",
		"Username": "admin",
		"Password": "secret",
		"Confirm":  true,
		"Reboot":   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	for _, want := range []string{
		`label-push 'SIP disable'`,
		`type-keycodes 'csrutil disable'`,
		`'Authorized user' 'admin'`,
		`'Password' 'secret'`,
		`[text-visible:System+Integrity+Protection+is+off.] type-keycodes 'reboot'`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("rendered template missing %q\n%s", want, text)
		}
	}
}

func TestWriteVZScriptForSIP(t *testing.T) {
	tmpDir := t.TempDir()
	script, err := generateSIPVZScript("enable", "admin", "secret", false, true)
	if err != nil {
		t.Fatal(err)
	}

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
