package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWorkspaceRuntimeExitGenerationBinding(t *testing.T) {
	owner := workspaceRuntimeOwner{Generation: "0123456789abcdef0123456789abcdef", PID: 123, StartedAt: time.Now().UTC().Format(time.RFC3339Nano), Directory: t.TempDir()}
	base := workspaceRuntimeExit{Generation: owner.Generation, PID: owner.PID, StartedAt: owner.StartedAt, EndedAt: time.Now().UTC().Format(time.RFC3339Nano), ExitCode: 0}
	for _, tt := range []struct {
		name   string
		change func(*workspaceRuntimeExit)
		want   bool
	}{
		{"matching", func(*workspaceRuntimeExit) {}, true},
		{"generation", func(s *workspaceRuntimeExit) { s.Generation = "ffffffffffffffffffffffffffffffff" }, false},
		{"pid", func(s *workspaceRuntimeExit) { s.PID++ }, false},
		{"start", func(s *workspaceRuntimeExit) { s.StartedAt = time.Now().UTC().Format(time.RFC3339Nano) }, false},
		{"failed", func(s *workspaceRuntimeExit) { s.ExitCode = 1 }, false},
		{"malformed-end", func(s *workspaceRuntimeExit) { s.EndedAt = "invalid" }, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := base
			tt.change(&s)
			data, _ := json.Marshal(s)
			if err := os.WriteFile(filepath.Join(owner.Directory, "exit.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			if got := workspaceRuntimeExited(owner); got != tt.want {
				t.Fatalf("exited=%v", got)
			}
		})
	}
}

func TestWorkspaceRuntimeReceiptRejectsMalformed(t *testing.T) {
	for _, data := range []string{`{"pid":1,"unknown":true}`, `{} {}`, string(make([]byte, 8193))} {
		p := filepath.Join(t.TempDir(), "running.json")
		os.WriteFile(p, []byte(data), 0600)
		var owner workspaceRuntimeOwner
		if readWorkspaceRuntimeReceipt(p, &owner) == nil {
			t.Fatal("accepted malformed receipt")
		}
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "target"), []byte(`{}`), 0600)
	os.Symlink("target", filepath.Join(dir, "running.json"))
	var owner workspaceRuntimeOwner
	if readWorkspaceRuntimeReceipt(filepath.Join(dir, "running.json"), &owner) == nil {
		t.Fatal("accepted symlink")
	}
}

func TestWorkspaceRuntimeOwnerCapture(t *testing.T) {
	guestDir := t.TempDir()
	guest, err := identifyTaskGuest(guestDir)
	if err != nil {
		t.Fatal(err)
	}
	generation := "0123456789abcdef0123456789abcdef"
	state := taskDisposition{Guest: &guest, Generation: generation}
	dir := filepath.Join(guestDir, "workspace-runtime-test")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(guestDir, "workspace-runtime-diagnostics.json"), []byte(`{"directory":"workspace-runtime-test"}`), 0600)
	start := processStartedAt(os.Getpid())
	if start.IsZero() {
		t.Fatal("process start unavailable")
	}
	owner := workspaceRuntimeOwner{Generation: generation, PID: os.Getpid(), StartedAt: start.UTC().Format(time.RFC3339Nano)}
	for _, tt := range []struct {
		name   string
		change func(*workspaceRuntimeOwner)
		want   bool
	}{
		{"matching", func(*workspaceRuntimeOwner) {}, true},
		{"stale-generation", func(s *workspaceRuntimeOwner) { s.Generation = "ffffffffffffffffffffffffffffffff" }, false},
		{"reused-pid", func(s *workspaceRuntimeOwner) { s.StartedAt = start.Add(-time.Second).UTC().Format(time.RFC3339Nano) }, false},
		{"unknown-pid", func(s *workspaceRuntimeOwner) { s.PID = -1 }, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := owner
			tt.change(&s)
			data, _ := json.Marshal(s)
			os.WriteFile(filepath.Join(dir, "running.json"), data, 0600)
			_, err := captureWorkspaceRuntimeOwner(state, generation)
			if (err == nil) != tt.want {
				t.Fatalf("capture error=%v", err)
			}
		})
	}
}
