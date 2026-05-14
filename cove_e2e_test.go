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
			name:     "runs_list_table_empty",
			args:     []string{"runs", "list"},
			wantExit: 0,
			// Header row is always printed even when empty.
			wantStdout: []string{"RUN ID", "STATUS"},
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
