package main

import (
	"strings"
	"testing"
)

func TestGenerateSIPVZScript_DisableWithPasswordConfirmReboot_Order(t *testing.T) {
	got, err := generateSIPVZScript("disable", "admin", "secret", true, true)
	if err != nil {
		t.Fatal(err)
	}

	wantOrder := []string{
		`wait-menu-text Utilities 180s`,
		`key cmd+shift+t`,
		`type-keycodes 'csrutil disable'`,
		`[text-visible:Are+you+sure] type-keycodes 'y'`,
		`[text-visible:Authorized+user] type-keycodes 'admin'`,
		`[text-visible:Password] type-keycodes 'secret'`,
		`type-keycodes 'reboot'`,
	}
	assertOrderedSnippets(t, got, wantOrder)
}

func TestGenerateSIPVZScript_DisableWithoutConfirm(t *testing.T) {
	got, err := generateSIPVZScript("disable", "", "secret", false, true)
	if err != nil {
		t.Fatal(err)
	}
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
