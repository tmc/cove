package main

import (
	"reflect"
	"testing"
)

func TestExtractCtlSubcommandFlags(t *testing.T) {
	tests := []struct {
		name        string
		in          []string
		wantArgs    []string
		wantOutput  string
		wantDaemon  bool
		seedOutput  string
	}{
		{
			name:     "plain command",
			in:       []string{"agent-exec", "ls"},
			wantArgs: []string{"agent-exec", "ls"},
		},
		{
			name:     "dash dash separates ctl flags from payload",
			in:       []string{"agent-exec", "--", "ls"},
			wantArgs: []string{"agent-exec", "ls"},
		},
		{
			name:     "dash dash preserves flag-shaped payload args",
			in:       []string{"agent-exec", "--", "ls", "--color=auto"},
			wantArgs: []string{"agent-exec", "ls", "--color=auto"},
		},
		{
			name:       "ctl --daemon before payload",
			in:         []string{"agent-exec", "--daemon", "whoami"},
			wantArgs:   []string{"agent-exec", "whoami"},
			wantDaemon: true,
		},
		{
			name:       "ctl --daemon plus dash dash",
			in:         []string{"agent-exec", "--daemon", "--", "ls", "-la"},
			wantArgs:   []string{"agent-exec", "ls", "-la"},
			wantDaemon: true,
		},
		{
			name:     "payload --daemon after dash dash is preserved",
			in:       []string{"agent-exec", "--", "myprog", "--daemon"},
			wantArgs: []string{"agent-exec", "myprog", "--daemon"},
		},
		{
			name:       "ctl -o before subcommand payload",
			in:         []string{"screenshot", "-o", "/tmp/x.png"},
			wantArgs:   []string{"screenshot"},
			wantOutput: "/tmp/x.png",
		},
		{
			name:       "payload -o after dash dash is preserved",
			in:         []string{"agent-exec", "--", "tar", "-o", "out.tar"},
			wantArgs:   []string{"agent-exec", "tar", "-o", "out.tar"},
			wantOutput: "",
		},
		{
			name:     "lone dash dash is consumed",
			in:       []string{"agent-exec", "--"},
			wantArgs: []string{"agent-exec"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := tc.seedOutput
			got, daemon := extractCtlSubcommandFlags(append([]string(nil), tc.in...), &out)
			if !reflect.DeepEqual(got, tc.wantArgs) {
				t.Errorf("args = %#v, want %#v", got, tc.wantArgs)
			}
			if daemon != tc.wantDaemon {
				t.Errorf("daemon = %v, want %v", daemon, tc.wantDaemon)
			}
			if out != tc.wantOutput {
				t.Errorf("output = %q, want %q", out, tc.wantOutput)
			}
		})
	}
}
