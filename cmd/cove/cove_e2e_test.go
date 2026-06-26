// cove_e2e_test.go - golden-path end-to-end tests for top-level cove
// subcommands surfaced by R76 (version, image, vm, pin, runs).
//
// Like doctor_e2e_test.go, these tests build the cove binary and run
// it against a synthetic HOME so they don't touch the developer's
// ~/.vz/ tree or require a real VM, signing identity, or registry.
// Subcommands that need a live VM, network, or TCC grant are out of
// scope.

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
)

func TestCoveSubcommandsE2E(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cove is darwin-only")
	}
	bin := doctorE2EBinary(t)
	home := t.TempDir()

	cases := []struct {
		name       string
		args       []string
		wantExit   int
		wantStdout []string
		wantStderr []string
		ndjson     bool // stdout is NDJSON (zero or more JSON values, one per line)
	}{
		{
			name:       "version",
			args:       []string{"version"},
			wantExit:   0,
			wantStdout: []string{"cove"},
		},
		{
			name:     "image_list_empty",
			args:     []string{"image", "list"},
			wantExit: 0,
			// Fresh HOME has no images. Output is human-formatted.
			wantStdout: []string{"No images found", "cove up -user <name>"},
		},
		{
			name:       "vm_tree_json_empty",
			args:       []string{"vm", "tree", "--json"},
			wantExit:   0,
			wantStdout: []string{"[]"},
			ndjson:     true,
		},
		{
			name:     "pin_no_args_usage",
			args:     []string{"pin"},
			wantExit: 1,
			// pin requires an object ref; with no args it must print usage
			// to stderr. This pins the contract that a bare invocation is
			// a clear error, not a crash.
			wantStderr: []string{"usage: cove pin"},
		},
		{
			name:     "runs_list_ndjson_empty",
			args:     []string{"runs", "list", "--json"},
			wantExit: 0,
			// Fresh HOME has no run history; NDJSON of zero records is
			// the empty string, which is a valid (empty) NDJSON stream.
			ndjson: true,
		},
		{
			name:       "runs_list_table_empty",
			args:       []string{"runs", "list"},
			wantExit:   0,
			wantStdout: []string{"No runs found.", "cove runs list"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(bin, tc.args...)
			cmd.Env = append(os.Environ(), "HOME="+home)
			cmd.Stdin = strings.NewReader("")
			var stdout, stderr strings.Builder
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			gotExit := 0
			if err != nil {
				if ee, ok := err.(*exec.ExitError); ok {
					gotExit = ee.ExitCode()
				} else {
					t.Fatalf("run %v: %v", tc.args, err)
				}
			}
			if tc.wantExit >= 0 && gotExit != tc.wantExit {
				t.Errorf("exit: got %d, want %d\nstdout:\n%s\nstderr:\n%s",
					gotExit, tc.wantExit, stdout.String(), stderr.String())
			}
			for _, want := range tc.wantStdout {
				if !strings.Contains(stdout.String(), want) {
					t.Errorf("stdout missing %q\nstdout:\n%s", want, stdout.String())
				}
			}
			for _, want := range tc.wantStderr {
				if !strings.Contains(stderr.String(), want) {
					t.Errorf("stderr missing %q\nstderr:\n%s", want, stderr.String())
				}
			}
			if tc.ndjson {
				assertNDJSON(t, tc.name, stdout.String())
			}
		})
	}
}

func TestNestedHelpDoesNotTreatHelpAsOperand(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cove is darwin-only")
	}
	bin := doctorE2EBinary(t)
	home := t.TempDir()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "agent sandbox doctor", args: []string{"agent-sandbox", "doctor", "-h"}, want: "Usage: cove agent-sandbox doctor"},
		{name: "bench competitive", args: []string{"bench", "competitive", "-h"}, want: "Usage: cove bench competitive"},
		{name: "daemon start", args: []string{"daemon", "start", "-h"}, want: "Usage: cove daemon start"},
		{name: "storage prune", args: []string{"storage", "prune", "-h"}, want: "Usage: cove storage prune"},
		{name: "storage budget get", args: []string{"storage", "budget", "get", "-h"}, want: "Usage: cove storage budget get"},
		{name: "image build", args: []string{"image", "build", "-h"}, want: "Usage: cove image build"},
		{name: "image gc", args: []string{"image", "gc", "-h"}, want: "Usage: cove image gc"},
		{name: "image tag", args: []string{"image", "tag", "-h"}, want: "Usage: cove image tag"},
		{name: "security bookmark store save", args: []string{"security", "bookmark-store", "save", "-h"}, want: "Usage: cove security bookmark-store save"},
		{name: "security probe sandbox", args: []string{"security", "probe-sandbox", "-h"}, want: "Usage: cove security probe-sandbox"},
		{name: "fleet add", args: []string{"fleet", "add", "-h"}, want: "Usage: cove fleet add"},
		{name: "fleet metrics", args: []string{"fleet", "metrics", "-h"}, want: "Usage: cove fleet metrics"},
		{name: "fleet image push", args: []string{"fleet", "image", "push", "-h"}, want: "Usage: cove fleet image push"},
		{name: "trace start", args: []string{"trace", "start", "-h"}, want: "Usage: cove trace start"},
		{name: "trace stop", args: []string{"trace", "stop", "-h"}, want: "Usage: cove trace stop"},
		{name: "trace export", args: []string{"trace", "export", "-h"}, want: "Usage: cove trace export"},
		{name: "network logs", args: []string{"network", "logs", "-h"}, want: "Usage: cove network logs"},
		{name: "doctor sckit spike", args: []string{"doctor", "sckit-spike", "-h"}, want: "Usage: cove doctor sckit-spike"},
		{name: "disk resize", args: []string{"disk", "resize", "-h"}, want: "Usage: cove disk resize"},
		{name: "disk snapshot save", args: []string{"disk-snapshot", "save", "-h"}, want: "Usage: cove disk-snapshot save"},
		{name: "snapshot save", args: []string{"snapshot", "save", "-h"}, want: "Usage: cove snapshot save"},
		{name: "vm delete", args: []string{"vm", "delete", "-h"}, want: "Usage: cove vm delete"},
		{name: "image history", args: []string{"image", "history", "-h"}, want: "Usage: cove image history"},
		{name: "image push", args: []string{"image", "push", "-h"}, want: "Usage: cove image push"},
		{name: "image pull", args: []string{"image", "pull", "-h"}, want: "Usage: cove image pull"},
		{name: "image load", args: []string{"image", "load", "-h"}, want: "Usage: cove image load"},
		{name: "image prune", args: []string{"image", "prune", "-h"}, want: "Usage: cove image prune"},
		{name: "disk snapshot run", args: []string{"disk-snapshot", "run", "-h"}, want: "Usage: cove disk-snapshot"},
		{name: "shared folder add", args: []string{"shared-folder", "add", "-h"}, want: "Usage: cove shared-folder"},
		{name: "template save", args: []string{"template", "save", "-h"}, want: "Usage: cove template save"},
		{name: "pit run", args: []string{"pit", "run", "-h"}, want: "Usage: cove pit"},
		{name: "snapshot save", args: []string{"snapshot", "save", "-h"}, want: "Usage: cove snapshot"},
		{name: "policy show", args: []string{"policy", "dummy", "show", "-h"}, want: "Usage: cove policy <vm> show"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(bin, tt.args...)
			cmd.Env = append(os.Environ(), "HOME="+home)
			var stdout, stderr strings.Builder
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			if err != nil {
				t.Fatalf("run %v: %v\nstdout:\n%s\nstderr:\n%s", tt.args, err, stdout.String(), stderr.String())
			}
			out := stdout.String() + stderr.String()
			if !strings.Contains(out, tt.want) {
				t.Fatalf("output missing %q\nstdout:\n%s\nstderr:\n%s", tt.want, stdout.String(), stderr.String())
			}
		})
	}
}

// assertNDJSON validates that stdout is a valid NDJSON stream: zero or
// more JSON values, one per non-blank line. Empty streams are valid.
func assertNDJSON(t *testing.T, name, stdout string) {
	t.Helper()
	for i, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var v any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			t.Errorf("%s: line %d not valid JSON: %v\nline: %s", name, i+1, err, line)
		}
	}
}
