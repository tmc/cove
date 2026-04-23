package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSetupRollbackSnapshotCloneUsesInjectedClone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	source := filepath.Join(GetVMBaseDir(), "research-base")
	if err := os.MkdirAll(filepath.Join(source, "disk-snapshots", "clean-base"), 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{
		"disk.img": []byte("live-disk"),
		"aux.img":  []byte("aux"),
		filepath.Join("disk-snapshots", "clean-base", "disk.img"): []byte("snapshot-disk"),
	} {
		path := filepath.Join(source, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
	}

	fixedNow := time.Date(2026, 4, 22, 12, 34, 56, 0, time.Local)
	var gotOpts CloneOptions
	clone := func(opts CloneOptions) error {
		gotOpts = opts
		target := GetVMPath(opts.Target)
		if err := os.MkdirAll(target, 0755); err != nil {
			return err
		}
		return os.WriteFile(proxyStatePath(target), []byte("{}\n"), 0644)
	}

	got, err := SetupRollbackSnapshotClone(RollbackSnapshotCloneOptions{
		Source:   "research-base",
		Snapshot: "clean-base",
		Now: func() time.Time {
			return fixedNow
		},
		Clone: clone,
	})
	if err != nil {
		t.Fatalf("SetupRollbackSnapshotClone() error = %v", err)
	}

	wantName := "research-base-d-20260422-123456"
	if got.Name != wantName {
		t.Fatalf("SetupRollbackSnapshotClone() name = %q, want %q", got.Name, wantName)
	}
	if got.Path != GetVMPath(wantName) {
		t.Fatalf("SetupRollbackSnapshotClone() path = %q, want %q", got.Path, GetVMPath(wantName))
	}
	if got.Source != "research-base" {
		t.Fatalf("SetupRollbackSnapshotClone() source = %q, want %q", got.Source, "research-base")
	}
	if !got.CreatedAt.Equal(fixedNow) {
		t.Fatalf("SetupRollbackSnapshotClone() createdAt = %v, want %v", got.CreatedAt, fixedNow)
	}
	if gotOpts.Source != "research-base" || gotOpts.Target != wantName || !gotOpts.Linked || gotOpts.CopyMachineID {
		t.Fatalf("SetupRollbackSnapshotClone() clone opts = %#v", gotOpts)
	}
	wantDisk := filepath.Join(source, "disk-snapshots", "clean-base", "disk.img")
	if resolvePath(gotOpts.SourceDiskPath) != resolvePath(wantDisk) {
		t.Fatalf("SetupRollbackSnapshotClone() source disk = %q, want %q", gotOpts.SourceDiskPath, wantDisk)
	}
	if _, err := os.Stat(proxyStatePath(got.Path)); !os.IsNotExist(err) {
		t.Fatalf("SetupRollbackSnapshotClone() left proxy state behind: %v", err)
	}
}
