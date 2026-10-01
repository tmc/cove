package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDiskSnapshotSaveRejectsMissingAndActiveSource(t *testing.T) {
	for _, name := range []string{"missing", "active"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if name == "active" {
				if err := os.WriteFile(filepath.Join(dir, "disk.img"), []byte("disk"), 0600); err != nil {
					t.Fatal(err)
				}
				lock, err := AcquireRunLock(dir)
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Release()
			}
			mgr := NewDiskSnapshotManager(dir)
			if err := mgr.Save("refused", DiskSnapshotSystem, ""); err == nil {
				t.Fatal("Save accepted missing or active disk")
			}
			if _, err := os.Lstat(mgr.snapshotDir("refused")); !os.IsNotExist(err) {
				t.Fatalf("snapshot published: %v", err)
			}
		})
	}
}

func snapshotSaveTestDeps() diskSnapshotSaveDeps {
	return diskSnapshotSaveDeps{ownership: stoppedResizeTestDeps(emptyVMProcessRunner())}
}

func TestDiskSnapshotSavePublicationFailures(t *testing.T) {
	for _, name := range []string{"copy", "metadata", "publication", "short-copy", "changed-source"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "disk.img")
			if err := os.WriteFile(src, []byte("disk bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			mgr := NewDiskSnapshotManager(dir)
			deps := snapshotSaveTestDeps()
			switch name {
			case "copy":
				deps.clone = func(_, dst string) error { os.WriteFile(dst, []byte("partial"), 0600); return os.ErrPermission }
			case "short-copy":
				deps.clone = func(_, dst string) error { return os.WriteFile(dst, []byte("short"), 0600) }
			case "changed-source":
				deps.clone = func(src, dst string) error {
					if err := copyFile(src, dst); err != nil {
						return err
					}
					return os.WriteFile(src, []byte("changed and longer"), 0600)
				}
			case "metadata":
				deps.writeMetadata = func(path string, _ []byte, _ os.FileMode) error {
					os.WriteFile(path, []byte("partial"), 0600)
					return os.ErrPermission
				}
			case "publication":
				deps.publish = func(_, _ string, _ bool) error { return os.ErrPermission }
			}
			if err := mgr.save("failed", DiskSnapshotSystem, "", deps); err == nil {
				t.Fatal("Save succeeded")
			}
			if _, err := os.Lstat(mgr.snapshotDir("failed")); !os.IsNotExist(err) {
				t.Fatalf("failed snapshot published: %v", err)
			}
			stages, err := filepath.Glob(filepath.Join(dir, ".cove-disk-snapshot-stage-*"))
			if err != nil || len(stages) != 0 {
				t.Fatalf("staging residue %v: %v", stages, err)
			}
			lock, err := AcquireRunLock(dir)
			if err != nil {
				t.Fatalf("run lock leaked: %v", err)
			}
			lock.Release()
		})
	}
}

func TestDiskSnapshotSavePrivateStageAndExclusivePublication(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "disk.img")
	if err := os.WriteFile(src, []byte("disk"), 0600); err != nil {
		t.Fatal(err)
	}
	abandoned := filepath.Join(dir, ".cove-disk-snapshot-stage-interrupted")
	if err := os.Mkdir(abandoned, 0700); err != nil {
		t.Fatal(err)
	}
	mgr := NewDiskSnapshotManager(dir)
	deps := snapshotSaveTestDeps()
	deps.clone = func(src, dst string) error {
		info, err := os.Stat(filepath.Dir(dst))
		if err != nil || info.Mode().Perm() != 0700 {
			t.Fatalf("stage permissions: %v %v", info, err)
		}
		if _, err := os.Stat(abandoned); !os.IsNotExist(err) {
			t.Fatalf("abandoned stage not cleaned: %v", err)
		}
		listed, err := mgr.List()
		if err != nil || len(listed) != 0 {
			t.Fatalf("partial snapshot listed: %v %v", listed, err)
		}
		if lock, err := AcquireRunLock(dir); err == nil {
			lock.Release()
			t.Fatal("save did not retain run lock")
		}
		return copyFile(src, dst)
	}
	deps.publish = func(stage, dest string, overwrite bool) error {
		if err := os.Mkdir(dest, 0700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dest, "winner"), []byte("keep"), 0600); err != nil {
			return err
		}
		return publishCopy(stage, dest, overwrite)
	}
	if err := mgr.save("race", DiskSnapshotSystem, "", deps); !errors.Is(err, ErrDiskSnapshotExists) {
		t.Fatalf("error = %v, want exists", err)
	}
	if got, err := os.ReadFile(filepath.Join(mgr.snapshotDir("race"), "winner")); err != nil || string(got) != "keep" {
		t.Fatalf("concurrent winner changed: %q %v", got, err)
	}
}

func TestDiskSnapshotSaveRejectsUncertainOwnership(t *testing.T) {
	for _, name := range []string{"open", "holder-probe-error", "process-probe-error", "invalid-target", "directory"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			disk := filepath.Join(dir, "disk.img")
			if name == "directory" {
				os.Mkdir(disk, 0700)
			} else if err := os.WriteFile(disk, []byte("disk"), 0600); err != nil {
				t.Fatal(err)
			}
			deps := snapshotSaveTestDeps()
			target := DiskSnapshotSystem
			switch name {
			case "open":
				deps.ownership.fileHolders = func(string) ([]int, error) { return []int{123}, nil }
			case "holder-probe-error":
				deps.ownership.fileHolders = func(string) ([]int, error) { return nil, os.ErrPermission }
			case "process-probe-error":
				deps.ownership.processes = commandVMProcessCollector{runner: fakeVMProcessRunner{err: map[string]error{"ps\x00-axo\x00pid=,ppid=,command=": os.ErrPermission}}}
			case "invalid-target":
				target = 0
			}
			mgr := NewDiskSnapshotManager(dir)
			if err := mgr.save("refused", target, "", deps); err == nil {
				t.Fatal("Save accepted uncertain ownership or invalid source")
			}
			if _, err := os.Lstat(mgr.snapshotDir("refused")); !os.IsNotExist(err) {
				t.Fatalf("snapshot published: %v", err)
			}
		})
	}
}

func TestDiskSnapshotSaveRejectsOpenSource(t *testing.T) {
	dir := t.TempDir()
	disk := filepath.Join(dir, "disk.img")
	if err := os.WriteFile(disk, []byte("disk"), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(disk, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	mgr := NewDiskSnapshotManager(dir)
	deps := snapshotSaveTestDeps()
	deps.ownership.fileHolders = openFileHolderPIDs
	if err := mgr.save("open", DiskSnapshotSystem, "", deps); err == nil {
		t.Fatal("Save accepted open source")
	}
	if _, err := os.Lstat(mgr.snapshotDir("open")); !os.IsNotExist(err) {
		t.Fatalf("snapshot published: %v", err)
	}
}
