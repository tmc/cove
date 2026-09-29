package main

import (
	"flag"
	"reflect"
	"testing"
)

func TestFlagSetTakesValue(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.Bool("b", false, "bool")
	fs.String("s", "", "string")
	fs.Int("n", 0, "int")

	got := flagSetTakesValue(fs)
	want := map[string]bool{
		"b": false,
		"s": true,
		"n": true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("flagSetTakesValue = %v, want %v", got, want)
	}
}

func TestMoveKnownFlagsFirst(t *testing.T) {
	takesValue := map[string]bool{
		"gui":   false,
		"force": false,
		"vm":    true,
		"cpu":   true,
	}

	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "empty",
			input: nil,
			want:  nil,
		},
		{
			name:  "positional only",
			input: []string{"foo"},
			want:  []string{"foo"},
		},
		{
			name:  "flag after positional bool",
			input: []string{"foo", "-gui"},
			want:  []string{"-gui", "foo"},
		},
		{
			name:  "flag before positional bool",
			input: []string{"-gui", "foo"},
			want:  []string{"-gui", "foo"},
		},
		{
			name:  "flag after positional value",
			input: []string{"foo", "-vm", "bar"},
			want:  []string{"-vm", "bar", "foo"},
		},
		{
			name:  "flag after positional with equal",
			input: []string{"foo", "-vm=bar"},
			want:  []string{"-vm=bar", "foo"},
		},
		{
			name:  "multiple flags and positionals",
			input: []string{"foo", "-gui", "bar", "-force"},
			want:  []string{"-gui", "-force", "foo", "bar"},
		},
		{
			name:  "terminator stops moving flags",
			input: []string{"foo", "--", "-gui"},
			want:  []string{"foo", "--", "-gui"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := moveKnownFlagsFirst(tt.input, takesValue)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("moveKnownFlagsFirst(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}
