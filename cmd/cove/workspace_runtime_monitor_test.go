package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWorkspaceRuntimeTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stderr-tail.log")
	w := &workspaceRuntimeTail{path: path}
	input := append(bytes.Repeat([]byte("a"), workspaceRuntimeTailLimit+10), []byte("final diagnostic")...)
	for _, p := range [][]byte{input[:20], input[20:]} {
		if n, err := w.Write(p); n != len(p) || err != nil {
			t.Fatalf("write = %d, %v", n, err)
		}
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, input[len(input)-workspaceRuntimeTailLimit:]) {
		t.Fatalf("tail differs: length %d, error %v", len(got), err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("diagnostic permissions: %v, %v", info, err)
	}
}

func TestWorkspaceRuntimeExitReceipt(t *testing.T) {
	for _, tt := range []struct {
		name string
		cmd  *exec.Cmd
		code int
	}{
		{"success", exec.Command("/bin/sh", "-c", "printf output; printf diagnostic >&2"), 0},
		{"failed", exec.Command("/bin/sh", "-c", "printf diagnostic >&2; exit 7"), 7},
		{"not started", exec.Command("/nonexistent-cove-runtime"), -1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			err := monitorWorkspaceRuntime(tt.cmd, dir)
			if (err == nil) != (tt.code == 0) {
				t.Fatalf("monitor error = %v", err)
			}
			data, err := os.ReadFile(filepath.Join(dir, "exit.json"))
			if err != nil {
				t.Fatal(err)
			}
			var state workspaceRuntimeExit
			if err := json.Unmarshal(data, &state); err != nil {
				t.Fatal(err)
			}
			if state.ExitCode != tt.code || state.StartedAt == "" || state.EndedAt == "" {
				t.Fatalf("exit receipt: %+v", state)
			}
			if tt.code >= 0 {
				got, err := os.ReadFile(filepath.Join(dir, "stderr-tail.log"))
				if err != nil || string(got) != "diagnostic" {
					t.Fatalf("stderr = %q, %v", got, err)
				}
			}
		})
	}
}

func TestWorkspaceRuntimeDiagnosticFailureDoesNotInterruptOwner(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "stderr-tail.log"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := monitorWorkspaceRuntime(exec.Command("/bin/sh", "-c", "printf diagnostic >&2"), dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "exit.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state workspaceRuntimeExit
	if err := json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	if state.DiagnosticsError == "" || state.ExitCode != 0 {
		t.Fatalf("receipt: %+v", state)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 2 {
		t.Fatalf("temporary files remain: %v, %v", entries, err)
	}
}
