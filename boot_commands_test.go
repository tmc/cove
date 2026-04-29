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

func TestKeyNameToCodePunctuation(t *testing.T) {
	// Lock in punctuation aliases. Shifted variants share keycodes with
	// their unshifted forms; callers supply the shift modifier separately.
	tests := []struct {
		name string
		want uint16
	}{
		{"slash", 44},
		{"question", 44},
		{"questionmark", 44},
		{"backslash", 42},
		{"pipe", 42},
		{"bar", 42},
		{"semicolon", 41},
		{"colon", 41},
		{"quote", 39},
		{"apostrophe", 39},
		{"doublequote", 39},
		{"minus", 27},
		{"underscore", 27},
		{"equals", 24},
		{"plus", 24},
		{"leftbracket", 33},
		{"leftbrace", 33},
		{"rightbracket", 30},
		{"rightbrace", 30},
		{"grave", 50},
		{"backtick", 50},
		{"tilde", 50},
		{"comma", 43},
		{"less", 43},
		{"period", 47},
		{"dot", 47},
		{"greater", 47},
		{"exclamation", 18},
		{"bang", 18},
		{"at", 19},
		{"hash", 20},
		{"pound", 20},
		{"dollar", 21},
		{"percent", 23},
		{"caret", 22},
		{"ampersand", 26},
		{"asterisk", 28},
		{"star", 28},
		{"leftparen", 25},
		{"rightparen", 29},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := keyNameToCode(tt.name); got != tt.want {
				t.Errorf("keyNameToCode(%q) = %d, want %d", tt.name, got, tt.want)
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

func TestStartupOptionsTilePointsStartAboveOCRLabel(t *testing.T) {
	const (
		width  = 1440
		height = 900
		optX   = 823
		optY   = 475
	)

	points := startupOptionsTilePoints(width, height, optX, optY)
	if len(points) == 0 {
		t.Fatal("startupOptionsTilePoints returned no points")
	}
	if points[0].X != optX || points[0].Y >= optY {
		t.Fatalf("first point = (%v, %v), want above OCR label", points[0].X, points[0].Y)
	}
	if points[0].Y != optY-0.09*height {
		t.Fatalf("first y = %v, want %v", points[0].Y, optY-0.09*height)
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
			want:   []string{"Authorized user", "Password", "System Integrity Protection is"},
		},
		{
			name:   "username prompt",
			needle: "Authorized user",
			want:   []string{"Password", "Unknown user", "System Integrity Protection is"},
		},
		{
			name:   "password prompt",
			needle: "Password",
			want:   []string{"Authentication failure", "System Integrity Protection is"},
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
			if containsString(got, "-bash-3.2#") {
				t.Fatalf("promptClearTexts(%q)=%v, should not use stale shell prompt as progress", tt.needle, got)
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
