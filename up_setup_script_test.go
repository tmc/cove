package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseSetupScript(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "empty",
			input: "",
			want:  nil,
		},
		{
			name:  "blank lines only",
			input: "\n\n\n",
			want:  nil,
		},
		{
			name:  "comments only",
			input: "# hello\n#world\n  # indented\n",
			want:  nil,
		},
		{
			name:  "single command",
			input: "echo hello\n",
			want:  []string{"echo hello"},
		},
		{
			name: "mixed",
			input: "# install tools\n" +
				"brew install jq\n" +
				"\n" +
				"# verify\n" +
				"jq --version\n",
			want: []string{"brew install jq", "jq --version"},
		},
		{
			name:  "preserves leading whitespace on commands",
			input: "  echo indented\n",
			want:  []string{"  echo indented"},
		},
		{
			name:  "strips carriage returns",
			input: "echo crlf\r\necho lf\n",
			want:  []string{"echo crlf", "echo lf"},
		},
		{
			name:  "no trailing newline",
			input: "echo a\necho b",
			want:  []string{"echo a", "echo b"},
		},
		{
			name:  "comment after command does not strip",
			input: "echo hi # not a comment\n",
			want:  []string{"echo hi # not a comment"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSetupScript(strings.NewReader(tt.input))
			if err != nil {
				t.Fatalf("parseSetupScript: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}
