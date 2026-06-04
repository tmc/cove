package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/cove/internal/vmconfig"
)

func TestCaptureAuxSidecarCopiesAuxImg(t *testing.T) {
	vmDir := t.TempDir()
	want := []byte("aux-at-snapshot-time")
	if err := os.WriteFile(filepath.Join(vmDir, "aux.img"), want, 0o644); err != nil {
		t.Fatalf("write aux.img: %v", err)
	}

	if err := captureAuxSidecar(vmDir, "clean"); err != nil {
		t.Fatalf("captureAuxSidecar: %v", err)
	}
	got, err := os.ReadFile(auxSnapshotPath(vmDir, "clean"))
	if err != nil {
		t.Fatalf("read aux sidecar: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("aux sidecar = %q, want %q", got, want)
	}
}

func TestCaptureAuxSidecarMissingAuxImg(t *testing.T) {
	if err := captureAuxSidecar(t.TempDir(), "clean"); err == nil {
		t.Fatal("captureAuxSidecar without aux.img returned nil")
	}
}

func TestRemoveAuxSidecar(t *testing.T) {
	vmDir := t.TempDir()
	path := auxSnapshotPath(vmDir, "clean")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir snapshots: %v", err)
	}
	if err := os.WriteFile(path, []byte("aux"), 0o644); err != nil {
		t.Fatalf("write aux sidecar: %v", err)
	}
	if err := removeAuxSidecar(vmDir, "clean"); err != nil {
		t.Fatalf("removeAuxSidecar: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("aux sidecar still exists: %v", err)
	}
	if err := removeAuxSidecar(vmDir, "clean"); err != nil {
		t.Fatalf("removeAuxSidecar missing: %v", err)
	}
}

func TestForkVMWithSnapshotAuxSidecarReplay(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	parent := "aux-sidecar-parent"
	parentDir, _ := stageParentVMForSnapshotFork(t, parent, "clean")
	want := []byte("aux-at-snapshot-time")
	if err := os.WriteFile(auxSnapshotPath(parentDir, "clean"), want, 0o644); err != nil {
		t.Fatalf("write aux sidecar: %v", err)
	}

	if err := ForkVMWithSnapshot(ForkVMOptions{
		Parent:           parent,
		Child:            "aux-sidecar-child",
		Snapshot:         "clean",
		PreserveIdentity: true,
	}); err != nil {
		t.Fatalf("ForkVMWithSnapshot: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(vmconfig.BaseDir(), "aux-sidecar-child", "aux.img"))
	if err != nil {
		t.Fatalf("read child aux.img: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("child aux.img = %q, want %q", got, want)
	}
}

func TestForkVMWithSnapshotMissingAuxSidecarKeepsParentAux(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	parent := "no-aux-sidecar-parent"
	parentDir, _ := stageParentVMForSnapshotFork(t, parent, "clean")
	want, err := os.ReadFile(filepath.Join(parentDir, "aux.img"))
	if err != nil {
		t.Fatalf("read parent aux.img: %v", err)
	}

	if err := ForkVMWithSnapshot(ForkVMOptions{
		Parent:   parent,
		Child:    "no-aux-sidecar-child",
		Snapshot: "clean",
	}); err != nil {
		t.Fatalf("ForkVMWithSnapshot: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(vmconfig.BaseDir(), "no-aux-sidecar-child", "aux.img"))
	if err != nil {
		t.Fatalf("read child aux.img: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("child aux.img = %q, want parent aux %q", got, want)
	}
}
