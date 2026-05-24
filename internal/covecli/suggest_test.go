package covecli

import "testing"

func TestSuggest(t *testing.T) {
	choices := []string{
		"run", "install", "provision", "shared-folder", "shared-folders",
		"snapshot", "verify", "vzscript", "config",
	}
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
		{"xyzzy", ""},
		{"fakety-fake", ""},
	}
	for _, tt := range tests {
		if got := Suggest(tt.input, choices); got != tt.want {
			t.Errorf("Suggest(%q) = %q, want %q", tt.input, got, tt.want)
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
	for _, tt := range tests {
		if got := levenshtein(tt.a, tt.b); got != tt.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
