// doctor_e2e_test.go - golden-path end-to-end tests for the
// `cove doctor` command surface.
//
// These tests build the cove binary and invoke it with a synthetic
// HOME directory so they neither touch the developer's ~/.vz/ tree
// nor require a real VM, signing identity, or granted TCC services.
// Subcommands that would prompt for live consent (e.g. doctor
// tcc-preauth without -reset/-h) are deliberately out of scope.

package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

var (
	doctorE2EBinaryOnce sync.Once
	doctorE2EBinaryPath string
	doctorE2EBinaryErr  error
)

func doctorE2EBinary(t *testing.T) string {
	t.Helper()
	doctorE2EBinaryOnce.Do(func() {
		dir, err := os.MkdirTemp("", "cove-doctor-e2e-*")
		if err != nil {
			doctorE2EBinaryErr = err
			return
		}
		path := filepath.Join(dir, "cove-doctor-e2e")
		cmd := exec.Command("go", "build", "-o", path, ".")
		if out, err := cmd.CombinedOutput(); err != nil {
			doctorE2EBinaryErr = wrapBuildErr(err, out)
			return
		}
		doctorE2EBinaryPath = path
	})
	if doctorE2EBinaryErr != nil {
		t.Fatalf("build cove binary: %v", doctorE2EBinaryErr)
	}
	return doctorE2EBinaryPath
}

func wrapBuildErr(err error, out []byte) error {
	if len(out) == 0 {
		return err
	}
	return &buildError{err: err, out: string(out)}
}

type buildError struct {
	err error
	out string
}

func (b *buildError) Error() string { return b.err.Error() + "\n" + b.out }

func (b *buildError) Unwrap() error { return b.err }

func TestWrapBuildErrUnwraps(t *testing.T) {
	err := wrapBuildErr(os.ErrPermission, []byte("build output"))
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("errors.Is(%v, os.ErrPermission) = false", err)
	}
}

func TestDoctorE2E(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("cove doctor is darwin-only")
	}
	bin := doctorE2EBinary(t)
	home := t.TempDir()

	cases := []struct {
		name       string
		args       []string
		wantExit   int
		wantStdout []string // substrings required in stdout
		wantStderr []string // substrings required in stderr
		json       bool     // assert stdout is valid JSON
	}{
		{
			name:       "verify_alias_no_vm",
			args:       []string{"doctor"},
			wantExit:   1, // no VM exists in fresh HOME -> error to stderr, exit 1
			wantStderr: []string{"disk image not found"},
		},
		{
			name:       "tcc_preauth_help",
			args:       []string{"doctor", "tcc-preauth", "-h"},
			wantExit:   0,
			wantStderr: []string{"Usage: cove doctor tcc-preauth", "--reset"},
		},
		{
			name:       "tcc_preauth_reset",
			args:       []string{"doctor", "tcc-preauth", "-reset"},
			wantExit:   0,
			wantStdout: []string{"tcc preauth state cleared"},
		},
		{
			name:       "sckit_preauth_help",
			args:       []string{"doctor", "sckit-preauth", "-h"},
			wantExit:   0,
			wantStderr: []string{"Usage: cove doctor sckit-preauth"},
		},
		{
			name: "sckit_preauth_json",
			args: []string{"doctor", "sckit-preauth", "-json"},
			// exit 1 when SCKit unauthorized, 0 when authorized; both are
			// golden paths for the command itself. Don't pin exit.
			wantExit: -1,
			json:     true,
		},
		{
			name:     "action_doctor_json",
			args:     []string{"action", "doctor", "-json"},
			wantExit: -1, // 0 pass / 2 warn / 1 fail are all valid host shapes
			json:     true,
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
			if tc.json {
				assertJSONReport(t, tc.name, stdout.String())
			}
		})
	}
}

// assertJSONReport decodes stdout as JSON and applies command-specific
// shape checks. Doctor commands emit one JSON object on stdout; their
// structure differs across surfaces.
func assertJSONReport(t *testing.T, name, stdout string) {
	t.Helper()
	stdout = strings.TrimSpace(stdout)
	if stdout == "" {
		t.Fatalf("%s: empty stdout, expected JSON", name)
	}
	var generic map[string]any
	if err := json.Unmarshal([]byte(stdout), &generic); err != nil {
		t.Fatalf("%s: stdout is not valid JSON: %v\nstdout:\n%s", name, err, stdout)
	}
	switch name {
	case "sckit_preauth_json":
		for _, key := range []string{"SCKitAvailable", "ScreenRecordingAuthorized", "MacOSVersion"} {
			if _, ok := generic[key]; !ok {
				t.Errorf("sckit-preauth JSON missing key %q (got keys %v)", key, sortedKeys(generic))
			}
		}
	case "action_doctor_json":
		for _, key := range []string{"ok", "status", "checks"} {
			if _, ok := generic[key]; !ok {
				t.Errorf("action doctor JSON missing key %q (got keys %v)", key, sortedKeys(generic))
			}
		}
		checks, ok := generic["checks"].([]any)
		if !ok || len(checks) == 0 {
			t.Errorf("action doctor JSON checks: expected non-empty array, got %T", generic["checks"])
		}
		for i, c := range checks {
			m, ok := c.(map[string]any)
			if !ok {
				t.Errorf("action doctor checks[%d] is not an object: %T", i, c)
				continue
			}
			if _, ok := m["name"]; !ok {
				t.Errorf("action doctor checks[%d] missing name", i)
			}
			if _, ok := m["status"]; !ok {
				t.Errorf("action doctor checks[%d] missing status", i)
			}
		}
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
