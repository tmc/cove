package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestExtractCtlSubcommandFlags(t *testing.T) {
	tests := []struct {
		name       string
		in         []string
		wantArgs   []string
		wantOutput string
		wantDaemon bool
		wantStream bool
		seedOutput string
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
			name:       "ctl --stream before payload",
			in:         []string{"exec", "--stream", "tail", "-f", "/var/log/system.log"},
			wantArgs:   []string{"exec", "tail", "-f", "/var/log/system.log"},
			wantStream: true,
		},
		{
			name:     "payload --stream after dash dash is preserved",
			in:       []string{"exec", "--", "myprog", "--stream"},
			wantArgs: []string{"exec", "myprog", "--stream"},
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
			got, daemon, stream := extractCtlSubcommandFlags(append([]string(nil), tc.in...), &out)
			if !reflect.DeepEqual(got, tc.wantArgs) {
				t.Errorf("args = %#v, want %#v", got, tc.wantArgs)
			}
			if daemon != tc.wantDaemon {
				t.Errorf("daemon = %v, want %v", daemon, tc.wantDaemon)
			}
			if stream != tc.wantStream {
				t.Errorf("stream = %v, want %v", stream, tc.wantStream)
			}
			if out != tc.wantOutput {
				t.Errorf("output = %q, want %q", out, tc.wantOutput)
			}
		})
	}
}

func TestParseCtlScreenshotArgsFormat(t *testing.T) {
	tests := []struct {
		name       string
		in         []string
		wantFormat string
		wantOutput string
		wantErr    string
	}{
		{
			name:       "default jpeg",
			wantFormat: "jpeg",
		},
		{
			name:       "png",
			in:         []string{"-format", "png"},
			wantFormat: "png",
		},
		{
			name:       "jpg normalizes",
			in:         []string{"--format", "JPG"},
			wantFormat: "jpeg",
		},
		{
			name:       "equals form",
			in:         []string{"-format=PNG", "/tmp/screen.png"},
			wantFormat: "png",
			wantOutput: "/tmp/screen.png",
		},
		{
			name:    "missing format value",
			in:      []string{"-format"},
			wantErr: "requires",
		},
		{
			name:    "bad format",
			in:      []string{"-format", "gif"},
			wantErr: "must be",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			output := ""
			got, err := parseCtlScreenshotArgs(append([]string(nil), tc.in...), &output)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if got != tc.wantFormat {
				t.Fatalf("format = %q, want %q", got, tc.wantFormat)
			}
			if output != tc.wantOutput {
				t.Fatalf("output = %q, want %q", output, tc.wantOutput)
			}
		})
	}
}
