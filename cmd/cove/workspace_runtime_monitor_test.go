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
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	w := &workspaceRuntimeTail{root: root, name: filepath.Base(path)}
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
			err := monitorWorkspaceRuntime(tt.cmd, dir, "0123456789abcdef0123456789abcdef")
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
	if err := monitorWorkspaceRuntime(exec.Command("/bin/sh", "-c", "printf diagnostic >&2"), dir, "0123456789abcdef0123456789abcdef"); err != nil {
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
	if err != nil || len(entries) < 2 || len(entries) > 3 {
		t.Fatalf("temporary files remain: %v, %v", entries, err)
	}
}

func TestWorkspaceRuntimeDiagnosticsRetainAdmittedDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("COVE_STATE_DIR", root)
	path := filepath.Join(root, "vms", "guest.covevm")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	guest, diagnostic, name, err := admitWorkspaceRuntimeDiagnostics(root, "guest", path)
	if err != nil {
		t.Fatal(err)
	}
	defer guest.Close()
	defer diagnostic.Close()
	moved := path + "-moved"
	if err := os.Rename(path, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeWorkspaceRuntimeDiagnostic(guest, "workspace-runtime-diagnostics.json", []byte(name)); err != nil {
		t.Fatal(err)
	}
	if err := monitorWorkspaceRuntimeRoot(exec.Command("/bin/sh", "-c", "printf output; printf diagnostic >&2"), diagnostic, "0123456789abcdef0123456789abcdef", nil); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 0 {
		t.Fatalf("replacement written: %v %v", entries, err)
	}
	if _, err := os.Stat(filepath.Join(moved, name, "exit.json")); err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceRuntimeDiagnosticsMissingParentDoesNotRecreate(t *testing.T) {
	root := t.TempDir()
	t.Setenv("COVE_STATE_DIR", root)
	path := filepath.Join(root, "vms", "missing.covevm")
	if _, _, _, err := admitWorkspaceRuntimeDiagnostics(root, "missing", path); err == nil {
		t.Fatal("admitted missing guest")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("guest recreated: %v", err)
	}
	diagnosticPath := filepath.Join(root, "diagnostic")
	if err := os.Mkdir(diagnosticPath, 0700); err != nil {
		t.Fatal(err)
	}
	diagnostic, err := os.OpenRoot(diagnosticPath)
	if err != nil {
		t.Fatal(err)
	}
	defer diagnostic.Close()
	if err := os.Remove(diagnosticPath); err != nil {
		t.Fatal(err)
	}
	if err := writeWorkspaceRuntimeDiagnostic(diagnostic, "exit.json", []byte("{}")); err == nil {
		t.Fatal("wrote removed directory")
	}
	if _, err := os.Lstat(diagnosticPath); !os.IsNotExist(err) {
		t.Fatalf("diagnostic recreated: %v", err)
	}
}

func TestWorkspaceRuntimeCrashContextSurvivesLargeStack(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	w := &workspaceRuntimeTail{root: root, name: "stderr-tail.log", crash: new(workspaceRuntimeCrash)}
	if _, err := w.Write(bytes.Repeat([]byte("ordinary startup output\n"), 4096)); err != nil {
		t.Fatal(err)
	}
	for _, chunk := range [][]byte{[]byte("pa"), []byte("nic: original cause\n"), bytes.Repeat([]byte("goroutine stack frame\n"), 8192), []byte("last frame\n")} {
		if _, err := w.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	context, err := os.ReadFile(filepath.Join(dir, "stderr-crash-context.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(context) != workspaceRuntimeCrashLimit || !bytes.HasPrefix(context, []byte("panic: original cause\n")) {
		t.Fatalf("context len=%d prefix=%q", len(context), context[:40])
	}
	tail, err := os.ReadFile(filepath.Join(dir, "stderr-tail.log"))
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != workspaceRuntimeTailLimit || bytes.Contains(tail, []byte("original cause")) || !bytes.HasSuffix(tail, []byte("last frame\n")) {
		t.Fatal("tail no longer represents latest output")
	}
	if len(w.crash.pending) != 0 || len(w.crash.context) > workspaceRuntimeCrashLimit {
		t.Fatal("unbounded crash state")
	}
}

func TestWorkspaceRuntimeCrashMarkersFragmented(t *testing.T) {
	for _, marker := range []string{"panic:", "fatal error:", "SIGSEGV:", "SIGABRT:", "SIGBUS:", "runtime: out of memory", "runtime: goroutine stack exceeds ", "runtime: failed to create new OS thread", "unexpected fault address"} {
		t.Run(marker, func(t *testing.T) {
			for split := 1; split < len(marker); split++ {
				c := new(workspaceRuntimeCrash)
				c.append([]byte("ordinary output\n" + marker[:split]))
				c.append([]byte(marker[split:] + " cause\n"))
				if !bytes.Equal(c.context, []byte(marker+" cause\n")) {
					t.Fatalf("split %d context=%q", split, c.context)
				}
			}
		})
	}
	c := new(workspaceRuntimeCrash)
	for i := 0; i < 4096; i++ {
		if c.append([]byte("ordinary log message\n")) {
			t.Fatal("captured ordinary output")
		}
	}
	if len(c.context) != 0 || len(c.pending) > workspaceRuntimeCrashOverlap {
		t.Fatal("ordinary output retained unbounded")
	}
}

func TestWorkspaceRuntimeOrdinaryDiagnosticDoesNotConsumeCrashBudget(t *testing.T) {
	c := new(workspaceRuntimeCrash)
	c.append([]byte("runtime: initializing devices\n"))
	c.append(bytes.Repeat([]byte("runtime: polling status\n"), 1024))
	if len(c.context) != 0 {
		t.Fatalf("ordinary diagnostic captured: %q", c.context)
	}
	c.append([]byte("fatal err"))
	c.append([]byte("or: actual failure\n"))
	if !bytes.Equal(c.context, []byte("fatal error: actual failure\n")) {
		t.Fatalf("actual fatal header lost: %q", c.context)
	}
}
