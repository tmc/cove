package main

import (
	"strings"
	"testing"
)

func TestGenerateSIPVZScript_DisableWithPassword_Order(t *testing.T) {
	got, err := generateSIPVZScript("disable", "admin", "secret")
	if err != nil {
		t.Fatal(err)
	}

	wantOrder := []string{
		`wait-menu-text Utilities 180s`,
		`key cmd+shift+t`,
		`type-keycodes 'csrutil disable'`,
		`answer-visible -optional -timeout 45s`,
		`'Are you sure' 'y'`,
		`answer-visible -optional -skip-empty -timeout 5s`,
		`'Authorized user' $SIP_USER`,
		`answer-visible -optional -skip-empty -timeout 45s`,
		`'Password' $SIP_PASSWORD`,
		`[text-visible:System+Integrity+Protection+is+off.] type-keycodes 'reboot'`,
	}
	assertOrderedSnippets(t, got, wantOrder)
}

func TestGenerateSIPVZScript_DisableHandlesConfirmOptionally(t *testing.T) {
	got, err := generateSIPVZScript("disable", "", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "answer-visible -optional") {
		t.Fatalf("expected optional prompt handling\n%s", got)
	}
	if !strings.Contains(got, "'Are you sure' 'y'") {
		t.Fatalf("expected optional confirmation handling\n%s", got)
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
