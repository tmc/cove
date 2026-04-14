package main

import (
	"strings"
	"testing"
)

func TestParseKeySpec(t *testing.T) {
	tests := []struct {
		spec     string
		wantCode uint16
		wantMods uint
	}{
		{"return", 36, 0},
		{"tab", 48, 0},
		{"escape", 53, 0},
		{"space", 49, 0},
		{"cmd+q", 12, 1 << 20},
		{"cmd+shift+a", 0, (1 << 20) | (1 << 17)},
		{"ctrl+c", 8, 1 << 18},
	}

	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			code, mods := parseKeySpec(tt.spec)
			if code != tt.wantCode {
				t.Errorf("parseKeySpec(%q) code = %d, want %d", tt.spec, code, tt.wantCode)
			}
			if mods != tt.wantMods {
				t.Errorf("parseKeySpec(%q) mods = %d, want %d", tt.spec, mods, tt.wantMods)
			}
		})
	}
}

func TestIsValidKeySpec(t *testing.T) {
	tests := []struct {
		spec string
		want bool
	}{
		{spec: "a", want: true},
		{spec: "cmd+q", want: true},
		{spec: "ctrl+shift+z", want: true},
		{spec: "999", want: true},
		{spec: "hyper+q", want: false},
		{spec: "definitely-not-a-key", want: false},
		{spec: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			got := isValidKeySpec(tt.spec)
			if got != tt.want {
				t.Errorf("isValidKeySpec(%q) = %v, want %v", tt.spec, got, tt.want)
			}
		})
	}
}

func TestRecoveryAuthFailureMessage(t *testing.T) {
	got := recoveryAuthFailureMessage("Enter password")
	if got == "" {
		t.Fatal("recoveryAuthFailureMessage returned empty string")
	}
	for _, want := range []string{
		`"Enter password"`,
		"bootstrap recovery enabled",
		"diskutil apfs updatePreboot /",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("recoveryAuthFailureMessage() = %q, missing %q", got, want)
		}
	}
}

func TestPromptClearTexts(t *testing.T) {
	tests := []struct {
		name   string
		needle string
		want   []string
	}{
		{
			name:   "confirm prompt",
			needle: "[y/n]",
			want:   []string{"Authorized user", "Password", "System Integrity Protection is", "-bash-3.2#"},
		},
		{
			name:   "username prompt",
			needle: "Authorized user",
			want:   []string{"Password", "Unknown user", "System Integrity Protection is", "-bash-3.2#"},
		},
		{
			name:   "password prompt",
			needle: "Password",
			want:   []string{"Authentication failure", "System Integrity Protection is", "-bash-3.2#"},
		},
		{
			name:   "ordinary prompt",
			needle: "Continue",
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := promptClearTexts(tt.needle)
			if tt.want == nil && len(got) != 0 {
				t.Fatalf("promptClearTexts(%q)=%v, want no prompt-clear markers", tt.needle, got)
			}
			for _, want := range tt.want {
				if !containsString(got, want) {
					t.Fatalf("promptClearTexts(%q)=%v, missing %q", tt.needle, got, want)
				}
			}
		})
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestUnquote(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{`"hello"`, "hello"},
		{`hello`, "hello"},
		{`""`, ""},
		{`"a"`, "a"},
	}

	for _, tt := range tests {
		got := unquote(tt.input)
		if got != tt.want {
			t.Errorf("unquote(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSplitMenuItemArgs(t *testing.T) {
	menu, item := splitMenuItemArgs("Utilities|Terminal")
	if menu != "Utilities" || item != "Terminal" {
		t.Fatalf("splitMenuItemArgs simple = (%q, %q), want (%q, %q)", menu, item, "Utilities", "Terminal")
	}

	menu, item = splitMenuItemArgs(` Utilities | Terminal `)
	if menu != "Utilities" || item != "Terminal" {
		t.Fatalf("splitMenuItemArgs trimmed = (%q, %q), want (%q, %q)", menu, item, "Utilities", "Terminal")
	}

	menu, item = splitMenuItemArgs("Utilities")
	if menu != "" || item != "" {
		t.Fatalf("splitMenuItemArgs invalid = (%q, %q), want empty", menu, item)
	}
}

func TestSplitConditionalTypeArgs(t *testing.T) {
	needle, value := splitConditionalTypeArgs("Enter password|secret")
	if needle != "Enter password" || value != "secret" {
		t.Fatalf("splitConditionalTypeArgs simple = (%q, %q), want (%q, %q)", needle, value, "Enter password", "secret")
	}

	needle, value = splitConditionalTypeArgs("  Are you sure  |  y  ")
	if needle != "Are you sure" || value != "y" {
		t.Fatalf("splitConditionalTypeArgs trimmed = (%q, %q), want (%q, %q)", needle, value, "Are you sure", "y")
	}

	needle, value = splitConditionalTypeArgs("invalid")
	if needle != "" || value != "" {
		t.Fatalf("splitConditionalTypeArgs invalid = (%q, %q), want empty", needle, value)
	}
}
