package main

import "testing"

func TestLooksLikePermissionDialog(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{"empty", "", false},
		{"plain text", "hello world", false},
		{"only deny", "Don't Allow", false},
		{"would-like-to", "vz-agent would like to access Documents. Don't Allow OK", true},
		{"wants-to-control", "vz-agent wants to control Terminal. Don't Allow OK", true},
		{"curly apostrophe", "App would like to access Files. Don’t Allow Allow", true},
		{"wants access to", "App wants access to Accessibility. Don't Allow", true},
		{"deny but no request phrase", "Don't Allow this thing", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksLikePermissionDialog(tt.text); got != tt.want {
				t.Errorf("looksLikePermissionDialog(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestFlagTakesValue(t *testing.T) {
	for _, name := range []string{"socket", "timeout", "vm", "var", "env"} {
		if !flagTakesValue(name) {
			t.Errorf("flagTakesValue(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"terminal", "v", "auto-approve", "", "unknown"} {
		if flagTakesValue(name) {
			t.Errorf("flagTakesValue(%q) = true, want false", name)
		}
	}
}

func TestReorderArgsForFlags(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"empty", nil, nil},
		{"only flags", []string{"-v", "-terminal"}, []string{"-v", "-terminal"}},
		{"only positional", []string{"golang", "homebrew"}, []string{"golang", "homebrew"}},
		{"recipe then flags", []string{"golang", "-terminal", "-v"}, []string{"-terminal", "-v", "golang"}},
		{"flag-with-value space", []string{"-vm", "test1", "golang", "-v"}, []string{"-vm", "test1", "-v", "golang"}},
		{"flag-with-value equals", []string{"-vm=test1", "golang", "-v"}, []string{"-vm=test1", "-v", "golang"}},
		{"bool flag not consumed", []string{"-terminal", "golang"}, []string{"-terminal", "golang"}},
		{"unknown flag does not consume next", []string{"-foo", "bar", "golang"}, []string{"-foo", "bar", "golang"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reorderArgsForFlags(tt.in)
			if !equalStrings(got, tt.want) {
				t.Errorf("reorderArgsForFlags(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestScriptUsesUI(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want bool
	}{
		{"empty", "", false},
		{"only comments", "# hello\n# world\n", false},
		{"guest only", "guest-exec echo hi\nguest-ping\n", false},
		{"ocr-click", "guest-ping\nocr-click 'Continue'\n", true},
		{"with bang prefix", "!ocr-wait 'Done'\n", true},
		{"with question prefix", "?screenshot foo.png\n", true},
		{"detect-screen", "detect-screen\n", true},
		{"type", "type 'hello'\n", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := scriptUsesUI([]byte(tt.src)); got != tt.want {
				t.Errorf("scriptUsesUI(%q) = %v, want %v", tt.src, got, tt.want)
			}
		})
	}
}

func TestVZScriptGuestOSFromPlatform(t *testing.T) {
	if got := vzscriptGuestOSFromPlatform("linux"); got != "linux" {
		t.Errorf("linux: got %q, want linux", got)
	}
	if got := vzscriptGuestOSFromPlatform("darwin"); got != "darwin" {
		t.Errorf("darwin: got %q, want darwin", got)
	}
	if got := vzscriptGuestOSFromPlatform(""); got != "darwin" {
		t.Errorf("empty: got %q, want darwin (default)", got)
	}
	if got := vzscriptGuestOSFromPlatform("freebsd"); got != "darwin" {
		t.Errorf("unknown: got %q, want darwin (default)", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
