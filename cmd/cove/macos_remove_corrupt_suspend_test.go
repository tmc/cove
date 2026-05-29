package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveCorruptSuspendStateRemovesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "suspend.vmstate")
	if err := os.WriteFile(path, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	removeCorruptSuspendState(path)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected file removed, stat err=%v", err)
	}
}

func TestRemoveCorruptSuspendStateMissingIsNoop(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.vmstate")
	removeCorruptSuspendState(path)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected still missing, stat err=%v", err)
	}
}

func TestRemoveCorruptSuspendStateUnremovablePath(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// Passing a directory path to os.Remove on a non-empty dir would error,
	// but here the dir is empty so Remove succeeds. To force the error
	// branch, pass a path whose parent is not writable.
	parent := filepath.Join(dir, "ro")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(parent, "f")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(parent, 0o755) })
	// On macOS, root can still remove; skip if running as root.
	if os.Geteuid() == 0 {
		t.Skip("root can remove regardless of parent perms")
	}
	removeCorruptSuspendState(target)
	// File should still exist because parent is read-only.
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("expected file to remain, stat err=%v", err)
	}
}
