package main

import "testing"

func TestSuggestCommand(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"runn", "run"},
		{"isntall", "install"},
		{"intsall", "install"},
		{"provsion", "provision"},
		{"sharedfolder", "shared-folder"},
		{"shared-folders", "shared-folders"},
		{"snapshoot", "snapshot"},
		{"verfy", "verify"},
		{"vzsrcipt", "vzscript"},
		{"configg", "config"},
		// Completely unrelated strings should fall through (no suggestion).
		{"xyzzy", ""},
		{"fakety-fake", ""},
	}
	for _, tc := range tests {
		got := suggestCommand(tc.input)
		if got != tc.want {
			t.Errorf("suggestCommand(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestKnownCommandsCoverAliases(t *testing.T) {
	required := []string{
		"run", "install", "list", "shared-folder", "shared-folders",
		"vm", "rename", "export", "import", "config", "rm",
		"verify", "doctor", "provision", "inject",
	}
	set := make(map[string]bool, len(knownCommands))
	for _, k := range knownCommands {
		set[k] = true
	}
	for _, r := range required {
		if !set[r] {
			t.Errorf("knownCommands missing %q", r)
		}
	}
}

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"a", "", 1},
		{"", "abc", 3},
		{"kitten", "sitting", 3},
		{"run", "run", 0},
		{"run", "runn", 1},
	}
	for _, tc := range tests {
		if got := levenshtein(tc.a, tc.b); got != tc.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
