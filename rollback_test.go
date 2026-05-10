package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

func TestSetupRollbackSnapshotCloneEarlyRejects(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	t.Run("empty source", func(t *testing.T) {
		_, err := SetupRollbackSnapshotClone(RollbackSnapshotCloneOptions{Snapshot: "ok"})
		if err == nil || !strings.Contains(err.Error(), "source vm is required") {
			t.Fatalf("err = %v, want 'source vm is required'", err)
		}
	})

	t.Run("invalid snapshot name", func(t *testing.T) {
		_, err := SetupRollbackSnapshotClone(RollbackSnapshotCloneOptions{Source: "src", Snapshot: "../escape"})
		if err == nil {
			t.Fatal("invalid snapshot name = nil, want validation error")
		}
	})

	t.Run("source vm not found", func(t *testing.T) {
		_, err := SetupRollbackSnapshotClone(RollbackSnapshotCloneOptions{Source: "ghost-vm-r310", Snapshot: "checkpoint"})
		if err == nil || !strings.Contains(err.Error(), "source vm not found") {
			t.Fatalf("err = %v, want 'source vm not found'", err)
		}
	})
}

func TestSetupRollbackSnapshotCloneUsesInjectedClone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	source := filepath.Join(vmconfig.BaseDir(), "research-base")
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
		target := vmconfig.Path(opts.Target)
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
	if got.Path != vmconfig.Path(wantName) {
		t.Fatalf("SetupRollbackSnapshotClone() path = %q, want %q", got.Path, vmconfig.Path(wantName))
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
