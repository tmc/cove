//go:build darwin

package softreset

import "testing"

func TestParsePSLineBranches(t *testing.T) {
	tests := []struct {
		name        string
		line        string
		wantOK      bool
		wantPID     int
		wantUID     int
		wantCmdline string
	}{
		{name: "empty line", line: "", wantOK: false},
		{name: "two fields short", line: "1234 0", wantOK: false},
		{name: "non-numeric pid", line: "abc 0 launchd", wantOK: false},
		{name: "negative pid", line: "-1 0 launchd", wantOK: false},
		{name: "zero pid rejected", line: "0 0 swap", wantOK: false},
		{name: "non-numeric uid", line: "1234 root launchd", wantOK: false},
		{name: "negative uid", line: "1234 -5 launchd", wantOK: false},
		{
			name:        "minimal three fields",
			line:        "1234 0 launchd",
			wantOK:      true,
			wantPID:     1234,
			wantUID:     0,
			wantCmdline: "launchd",
		},
		{
			name:        "cmdline with spaces preserved",
			line:        "501 1000 /usr/bin/foo --flag value",
			wantOK:      true,
			wantPID:     501,
			wantUID:     1000,
			wantCmdline: "/usr/bin/foo --flag value",
		},
		{
			name:        "tab separators trimmed",
			line:        "\t42\t100\tcat\tfile",
			wantOK:      true,
			wantPID:     42,
			wantUID:     100,
			wantCmdline: "cat file",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parsePSLine(tt.line)
			if ok != tt.wantOK {
				t.Fatalf("parsePSLine(%q) ok = %v, want %v (got=%#v)", tt.line, ok, tt.wantOK, got)
			}
			if !ok {
				return
			}
			if got.PID != tt.wantPID {
				t.Fatalf("PID = %d, want %d", got.PID, tt.wantPID)
			}
			if got.UID != tt.wantUID {
				t.Fatalf("UID = %d, want %d", got.UID, tt.wantUID)
			}
			if got.Cmdline != tt.wantCmdline {
				t.Fatalf("Cmdline = %q, want %q", got.Cmdline, tt.wantCmdline)
			}
		})
	}
}
