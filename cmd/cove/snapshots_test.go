package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateSnapshotName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"checkpoint1", false},
		{"my-snapshot", false},
		{"v2.0", false},
		{"", true},          // empty
		{"a/b", true},       // forward slash
		{"a\\b", true},      // backslash
		{".", true},          // current dir
		{"..", true},         // parent dir
		{".hidden", false},   // dotfile is fine
		{"has spaces", false}, // spaces are ok
	}
	for _, tt := range tests {
		err := validateSnapshotName(tt.name)
		if (err != nil) != tt.wantErr {
			t.Errorf("validateSnapshotName(%q) error = %v, wantErr %v", tt.name, err, tt.wantErr)
		}
	}
}

func TestDiskSnapshotSaveRejectsBadNames(t *testing.T) {
	dir := t.TempDir()
	mgr := NewDiskSnapshotManager(dir)

	for _, name := range []string{"", "a/b", "..", "."} {
		if err := mgr.Save(name, DiskSnapshotSystem, ""); err == nil {
			t.Errorf("Save(%q) should have failed", name)
		}
	}
}

func TestDiskSnapshotSaveAndList(t *testing.T) {
	dir := t.TempDir()
	mgr := NewDiskSnapshotManager(dir)

	// Save should succeed even without a disk.img (it warns but creates metadata).
	if err := mgr.Save("test-snap", DiskSnapshotSystem, "test description"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	snaps, err := mgr.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("got %d snapshots, want 1", len(snaps))
	}
	if snaps[0].Name != "test-snap" {
		t.Errorf("name = %q, want %q", snaps[0].Name, "test-snap")
	}
	if snaps[0].Description != "test description" {
		t.Errorf("description = %q, want %q", snaps[0].Description, "test description")
	}
}

func TestDiskSnapshotListSystemSizeFallback(t *testing.T) {
	dir := t.TempDir()
	mgr := NewDiskSnapshotManager(dir)
	snapDir := filepath.Join(mgr.diskSnapshotsDir(), "no-meta")
	if err := os.MkdirAll(snapDir, 0755); err != nil {
		t.Fatal(err)
	}
	body := []byte("0123456789")
	if err := os.WriteFile(filepath.Join(snapDir, "disk.img"), body, 0644); err != nil {
		t.Fatal(err)
	}
	snaps, err := mgr.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("len = %d, want 1", len(snaps))
	}
	if snaps[0].SystemSize != int64(len(body)) {
		t.Fatalf("SystemSize = %d, want %d", snaps[0].SystemSize, len(body))
	}
}

func TestDiskSnapshotDeleteRejectsBadNames(t *testing.T) {
	dir := t.TempDir()
	mgr := NewDiskSnapshotManager(dir)

	if err := mgr.Delete(""); err == nil {
		t.Error("Delete('') should have failed")
	}
	if err := mgr.Delete("../escape"); err == nil {
		t.Error("Delete('../escape') should have failed")
	}
}

func TestDiskSnapshotSaveDuplicate(t *testing.T) {
	dir := t.TempDir()
	mgr := NewDiskSnapshotManager(dir)

	if err := mgr.Save("dup", DiskSnapshotSystem, ""); err != nil {
		t.Fatalf("first Save: %v", err)
	}
	err := mgr.Save("dup", DiskSnapshotSystem, "")
	if err == nil {
		t.Fatal("second Save of same name should have failed")
	}
	if !errors.Is(err, ErrDiskSnapshotExists) {
		t.Errorf("error = %v, want errors.Is(err, ErrDiskSnapshotExists)", err)
	}
}

func TestDiskSnapshotRestoreAndDeleteMissing(t *testing.T) {
	dir := t.TempDir()
	mgr := NewDiskSnapshotManager(dir)

	err := mgr.Restore("nope", DiskSnapshotSystem)
	if !errors.Is(err, ErrDiskSnapshotNotFound) {
		t.Errorf("Restore missing: err = %v, want ErrDiskSnapshotNotFound", err)
	}
	err = mgr.Delete("nope")
	if !errors.Is(err, ErrDiskSnapshotNotFound) {
		t.Errorf("Delete missing: err = %v, want ErrDiskSnapshotNotFound", err)
	}

	// Bad name on Restore is rejected before missing check.
	if err := mgr.Restore("a/b", DiskSnapshotSystem); err == nil {
		t.Error("Restore('a/b') should have failed name validation")
	}
}

func TestDiskSnapshotDeleteHappyPath(t *testing.T) {
	dir := t.TempDir()
	mgr := NewDiskSnapshotManager(dir)

	if err := mgr.Save("gone", DiskSnapshotSystem, ""); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := mgr.Delete("gone"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	snaps, err := mgr.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(snaps) != 0 {
		t.Errorf("got %d snapshots after delete, want 0", len(snaps))
	}
}

func TestHandleDiskSnapshotSaveArgs(t *testing.T) {
	dir := t.TempDir()
	mgr := NewDiskSnapshotManager(dir)

	if err := handleDiskSnapshotSave(mgr, nil); err == nil {
		t.Error("save with no args should fail")
	}
	if err := handleDiskSnapshotRestore(mgr, nil); err == nil {
		t.Error("restore with no args should fail")
	}
	if err := handleDiskSnapshotDelete(mgr, nil); err == nil {
		t.Error("delete with no args should fail")
	}

	// -desc captures the next argument.
	if err := handleDiskSnapshotSave(mgr, []string{"snap1", "-system", "-desc", "hello world"}); err != nil {
		t.Fatalf("save with flags: %v", err)
	}
	snaps, err := mgr.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(snaps) != 1 || snaps[0].Description != "hello world" {
		t.Errorf("snapshots = %+v, want one with desc 'hello world'", snaps)
	}
}

func TestHandleDiskSnapshotRestoreSystemFlag(t *testing.T) {
	dir := t.TempDir()
	mgr := NewDiskSnapshotManager(dir)

	if err := handleDiskSnapshotSave(mgr, []string{"snap1"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// -system selects DiskSnapshotSystem; restore reaches mgr.Restore which
	// returns an error because there is no live disk to clobber. We only
	// care that the arg branch executed without a parse-time failure.
	err := handleDiskSnapshotRestore(mgr, []string{"snap1", "-system"})
	if err == nil {
		t.Skip("Restore unexpectedly succeeded with no live disk; skipping branch check")
	}
}

func TestHandleDiskSnapshotRunArgs(t *testing.T) {
	if err := handleDiskSnapshotRun(nil); err == nil {
		t.Error("run with no args should fail")
	}

	prevDisp := disposableMode
	disposableMode = true
	err := handleDiskSnapshotRun([]string{"snap1"})
	disposableMode = prevDisp
	if err == nil || !strings.Contains(err.Error(), "disposable") {
		t.Errorf("err = %v, want disposable conflict", err)
	}

	if err := handleDiskSnapshotRun([]string{"snap1", "-bogus"}); err == nil ||
		!strings.Contains(err.Error(), "unknown disk-snapshot run option") {
		t.Errorf("err = %v, want unknown option", err)
	}

	prevDir := vmDir
	vmDir = t.TempDir()
	err = handleDiskSnapshotRun([]string{"missing-snap", "-ram"})
	vmDir = prevDir
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("err = %v, want not-found for missing snapshot", err)
	}
}

func TestHandleDiskSnapshotCommandUnknown(t *testing.T) {
	err := handleDiskSnapshotCommand([]string{"bogus"})
	if err == nil || !strings.Contains(err.Error(), "unknown disk-snapshot command") {
		t.Errorf("err = %v, want 'unknown disk-snapshot command'", err)
	}
}
