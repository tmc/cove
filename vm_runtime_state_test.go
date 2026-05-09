package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteVMRuntimePhase(t *testing.T) {
	tests := []struct {
		name      string
		vmDir     string
		state     string
		phase     string
		wantWrite bool
	}{
		{"empty vmDir is no-op", "", "starting", "", false},
		{"empty state is no-op", "vmdir", "  ", "phase", false},
		{"writes state only", "vmdir", "running", "", true},
		{"writes state and phase", "vmdir", "running", "boot", true},
		{"trims surrounding whitespace", "vmdir", "  paused  ", "  hibernate  ", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := tt.vmDir
			if dir == "vmdir" {
				dir = t.TempDir()
			}
			err := writeVMRuntimePhase(dir, tt.state, tt.phase)
			if err != nil {
				t.Fatalf("writeVMRuntimePhase: %v", err)
			}
			path := filepath.Join(dir, vmRuntimeStateFile)
			_, statErr := os.Stat(path)
			if tt.wantWrite && statErr != nil {
				t.Fatalf("expected runtime file at %s: %v", path, statErr)
			}
			if !tt.wantWrite && statErr == nil {
				t.Fatalf("did not expect file write for %s/%s", tt.state, tt.phase)
			}
			if !tt.wantWrite {
				return
			}
			rt, err := readVMRuntimeState(dir)
			if err != nil {
				t.Fatalf("readVMRuntimeState: %v", err)
			}
			if rt.PID != os.Getpid() {
				t.Errorf("PID = %d, want %d", rt.PID, os.Getpid())
			}
			if rt.UpdatedAt.IsZero() {
				t.Errorf("UpdatedAt zero")
			}
		})
	}
}

func TestReadVMRuntimeStateParseError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, vmRuntimeStateFile), []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := readVMRuntimeState(dir)
	if err == nil {
		t.Fatal("readVMRuntimeState returned nil error on bad JSON")
	}
}

func TestReadVMRuntimeStateMissing(t *testing.T) {
	dir := t.TempDir()
	_, err := readVMRuntimeState(dir)
	if err == nil {
		t.Fatal("readVMRuntimeState returned nil error on missing file")
	}
	if !os.IsNotExist(err) {
		t.Errorf("err = %v, want os.IsNotExist", err)
	}
}

func TestNoteVMRuntimeStateWritesFile(t *testing.T) {
	dir := t.TempDir()
	noteVMRuntimeState(dir, "starting")
	rt, err := readVMRuntimeState(dir)
	if err != nil {
		t.Fatalf("readVMRuntimeState: %v", err)
	}
	if rt.State != "starting" {
		t.Errorf("State = %q, want %q", rt.State, "starting")
	}
}

func TestNoteVMRuntimeStateSwallowsErrorOnBadDir(t *testing.T) {
	prev := verbose
	verbose = false
	t.Cleanup(func() { verbose = prev })
	noteVMRuntimeState(filepath.Join(t.TempDir(), "no-such-subdir", "vm"), "starting")
	noteVMRuntimePhase(filepath.Join(t.TempDir(), "no-such-subdir", "vm"), "running", "boot")
}
