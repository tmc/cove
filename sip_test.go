package main

import (
	"strings"
	"testing"
)

func TestGenerateSIPVZScript_DisableWithPasswordConfirmReboot_Order(t *testing.T) {
	got := generateSIPVZScript("disable", "admin", "secret", true, true)

	wantOrder := []string{
		`click-menu-item Utilities Terminal 60s`,
		`type 'csrutil disable'`,
		`[text-visible:Are+you+sure] type-keycodes 'y'`,
		`[text-visible:Authorized+user] type 'admin'`,
		`[text-visible:Password] type-keycodes 'secret'`,
		`[text-visible:System+Integrity+Protection+is+off.] type reboot`,
	}
	assertOrderedSnippets(t, got, wantOrder)
}

func TestGenerateSIPVZScript_DisableWithoutConfirm(t *testing.T) {
	got := generateSIPVZScript("disable", "", "secret", false, true)
	if strings.Contains(got, "Are you sure") {
		t.Fatalf("did not expect confirm prompt handling when confirm=false\n%s", got)
	}
	if strings.Contains(got, "[y/n]") {
		t.Fatalf("did not expect [y/n] prompt handling when confirm=false\n%s", got)
	}
}

func assertOrderedSnippets(t *testing.T, script string, want []string) {
	t.Helper()

	pos := 0
	for _, snippet := range want {
		i := strings.Index(script[pos:], snippet)
		if i < 0 {
			t.Fatalf("missing snippet %q\n%s", snippet, script)
		}
		pos += i + len(snippet)
	}
}
