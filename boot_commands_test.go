package main

import "testing"

func TestParseBootCommands(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int // number of commands
		wantErr bool
	}{
		{
			name:  "empty",
			input: "",
			want:  0,
		},
		{
			name:  "comments and blank lines",
			input: "# this is a comment\n\n# another comment\n",
			want:  0,
		},
		{
			name:  "wait command",
			input: `<wait 5s>`,
			want:  1,
		},
		{
			name:  "delay is alias for wait",
			input: `<delay 2s>`,
			want:  1,
		},
		{
			name:  "waitForText",
			input: `<waitForText "Continue">`,
			want:  1,
		},
		{
			name:  "click",
			input: `<click "Continue">`,
			want:  1,
		},
		{
			name:  "type",
			input: `<type "testuser">`,
			want:  1,
		},
		{
			name:  "key",
			input: `<key return>`,
			want:  1,
		},
		{
			name:  "key with modifier",
			input: `<key cmd+q>`,
			want:  1,
		},
		{
			name:  "screenshot",
			input: `<screenshot>`,
			want:  1,
		},
		{
			name: "multi command sequence",
			input: `# Setup Assistant automation
<wait 5s>
<waitForText "Select Your Country">
<click "Continue">
<wait 2s>
<type "testuser">
<key tab>
<key return>`,
			want: 7,
		},
		{
			name:    "invalid command",
			input:   `not a command`,
			wantErr: true,
		},
		{
			name:    "unknown command type",
			input:   `<explode now>`,
			wantErr: true,
		},
		{
			name:    "wait without duration",
			input:   `<wait>`,
			wantErr: true,
		},
		{
			name:    "wait with invalid duration",
			input:   `<wait tomorrow>`,
			wantErr: true,
		},
		{
			name:    "click without text",
			input:   `<click>`,
			wantErr: true,
		},
		{
			name:    "type without text",
			input:   `<type>`,
			wantErr: true,
		},
		{
			name:    "key without name",
			input:   `<key>`,
			wantErr: true,
		},
		{
			name:    "key with invalid name",
			input:   `<key totallyunknown>`,
			wantErr: true,
		},
		{
			name:    "key with invalid modifier",
			input:   `<key hyper+q>`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseBootCommands(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseBootCommands() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && len(got) != tt.want {
				t.Errorf("ParseBootCommands() got %d commands, want %d", len(got), tt.want)
			}
		})
	}
}

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
