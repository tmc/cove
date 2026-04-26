package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func sha256File(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatalf("hash %s: %v", path, err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TestForkVMDisk_CoWDivergence proves the fork primitive: a child cloned
// from a parent starts byte-identical, but writes to the child diverge from
// the parent without touching it. This is the core invariant that lets
// "disk-snapshot restore" preserve the snapshot while the live VM mutates
// its own disk (Model A in docs/designs/013-vm-fork.md).
func TestForkVMDisk_CoWDivergence(t *testing.T) {
	dir := t.TempDir()
	if !SupportsClonefile(dir) {
		t.Skip("filesystem does not support clonefile")
	}

	parent := filepath.Join(dir, "parent.img")
	child := filepath.Join(dir, "child.img")

	original := make([]byte, 64*1024)
	for i := range original {
		original[i] = byte(i % 251)
	}
	if err := os.WriteFile(parent, original, 0644); err != nil {
		t.Fatalf("write parent: %v", err)
	}

	parentHashBefore := sha256File(t, parent)

	if err := ForkVMDisk(parent, child); err != nil {
		t.Fatalf("ForkVMDisk: %v", err)
	}

	childHashAfterFork := sha256File(t, child)
	if childHashAfterFork != parentHashBefore {
		t.Fatalf("child differs from parent immediately after fork: parent=%s child=%s",
			parentHashBefore, childHashAfterFork)
	}

	f, err := os.OpenFile(child, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open child for write: %v", err)
	}
	if _, err := f.WriteAt([]byte{0xFF}, 17); err != nil {
		f.Close()
		t.Fatalf("write child: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close child: %v", err)
	}

	parentHashAfter := sha256File(t, parent)
	childHashAfter := sha256File(t, child)

	if parentHashAfter != parentHashBefore {
		t.Fatalf("parent mutated by child write: before=%s after=%s",
			parentHashBefore, parentHashAfter)
	}
	if childHashAfter == childHashAfterFork {
		t.Fatalf("child hash unchanged after write — divergence failed: %s", childHashAfter)
	}
}

func TestForkVMDisk_RejectsExistingChild(t *testing.T) {
	dir := t.TempDir()
	if !SupportsClonefile(dir) {
		t.Skip("filesystem does not support clonefile")
	}

	parent := filepath.Join(dir, "parent.img")
	child := filepath.Join(dir, "child.img")

	if err := os.WriteFile(parent, []byte("hi"), 0644); err != nil {
		t.Fatalf("write parent: %v", err)
	}
	if err := os.WriteFile(child, []byte("existing"), 0644); err != nil {
		t.Fatalf("write child: %v", err)
	}

	err := ForkVMDisk(parent, child)
	if err == nil {
		t.Fatal("expected error when child already exists")
	}
}

func TestForkVMDisk_RejectsMissingParent(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "missing.img")
	child := filepath.Join(dir, "child.img")

	err := ForkVMDisk(parent, child)
	if err == nil {
		t.Fatal("expected error when parent is missing")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected ErrNotExist, got: %v", err)
	}
}

func TestForkVMDisk_RejectsEmptyPaths(t *testing.T) {
	if err := ForkVMDisk("", "child"); err == nil {
		t.Error("expected error for empty parent")
	}
	if err := ForkVMDisk("parent", ""); err == nil {
		t.Error("expected error for empty child")
	}
}
