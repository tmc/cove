package main

import (
	"testing"

	"github.com/tmc/cove/internal/covecli"
)

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
		"run", "install", "list", "ls", "shared-folder", "shared-folders",
		"vm", "rename", "export", "import", "config", "rm",
		"verify", "doctor", "provision", "inject", "first-run", "support-bundle",
	}
	set := make(map[string]bool, len(commandNames()))
	for _, k := range commandNames() {
		set[k] = true
	}
	for _, r := range required {
		if !set[r] {
			t.Errorf("knownCommands missing %q", r)
		}
	}
}

func TestLookupCommandAliases(t *testing.T) {
	tests := []struct {
		name      string
		wantName  string
		wantClass covecli.Dispatch
	}{
		{"run", "run", covecli.DispatchLate},
		{"ls", "list", covecli.DispatchLate},
		{"inject", "inject", covecli.DispatchEarly},
		{"doctor", "verify", covecli.DispatchEarly},
		{"shared-folders", "shared-folder", covecli.DispatchEarly},
		{"first-run", "first-run", covecli.DispatchEarly},
		{"support-bundle", "support-bundle", covecli.DispatchEarly},
		{"action", "action", covecli.DispatchPreUI},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec, ok := lookupCommand(tt.name)
			if !ok {
				t.Fatalf("lookupCommand(%q) not found", tt.name)
			}
			if spec.Name != tt.wantName || spec.Dispatch != tt.wantClass {
				t.Fatalf("lookupCommand(%q) = (%q, %v), want (%q, %v)", tt.name, spec.Name, spec.Dispatch, tt.wantName, tt.wantClass)
			}
		})
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
