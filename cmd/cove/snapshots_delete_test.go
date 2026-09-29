package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHandleDiskSnapshotDeleteFlow covers the post-confirmation branches of
// handleDiskSnapshotDelete with -y: missing snapshot returns Delete's not-found error,
// and a staged snapshot directory is removed on success.
func TestHandleDiskSnapshotDeleteFlow(t *testing.T) {
	t.Run("missing snapshot returns not-found", func(t *testing.T) {
		dir := t.TempDir()
		mgr := NewDiskSnapshotManager(dir)
		err := handleDiskSnapshotDelete(mgr, []string{"-y", "ghost"})
		if err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("err = %v, want 'not found'", err)
		}
	})

	t.Run("happy path removes staged snapshot directory", func(t *testing.T) {
		dir := t.TempDir()
		mgr := NewDiskSnapshotManager(dir)
		snapDir := filepath.Join(dir, "disk-snapshots", "snap-a")
		if err := os.MkdirAll(snapDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(snapDir, "metadata.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := handleDiskSnapshotDelete(mgr, []string{"-y", "snap-a"}); err != nil {
			t.Fatalf("handleDiskSnapshotDelete: %v", err)
		}
		if _, err := os.Stat(snapDir); !os.IsNotExist(err) {
			t.Errorf("snap dir still exists: stat err = %v", err)
		}
	})

	t.Run("invalid name returns validation error", func(t *testing.T) {
		dir := t.TempDir()
		mgr := NewDiskSnapshotManager(dir)
		err := handleDiskSnapshotDelete(mgr, []string{"-y", "../escape"})
		if err == nil {
			t.Fatal("err = nil, want validation error for path-traversal name")
		}
	})
}
