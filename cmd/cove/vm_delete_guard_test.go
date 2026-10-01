package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tmc/cove/internal/mutationguard"
	"github.com/tmc/cove/internal/storagepins"
	"github.com/tmc/cove/internal/vmconfig"
)

func TestVMDeletionAdmissionRetainsProtectedGuest(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, string, *vmDeletionDeps)
		want  string
	}{
		{"pinned", func(t *testing.T, path string, _ *vmDeletionDeps) {
			pins := storagepins.New()
			if err := pins.Add("vm", "protected", time.Now()); err != nil {
				t.Fatal(err)
			}
			if err := storagepins.Save(coveRoot(), pins); err != nil {
				t.Fatal(err)
			}
		}, "pinned"},
		{"pin observation unknown", func(t *testing.T, _ string, _ *vmDeletionDeps) {
			if err := os.MkdirAll(coveRoot(), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(coveRoot(), storagepins.Filename), []byte("{"), 0600); err != nil {
				t.Fatal(err)
			}
		}, "load vm deletion pins"},
		{"held lock", func(t *testing.T, path string, _ *vmDeletionDeps) {
			lock, err := AcquireRunLock(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { lock.Release() })
		}, "lock vm deletion"},
		{"run lock symlink", func(t *testing.T, path string, _ *vmDeletionDeps) {
			external := filepath.Join(t.TempDir(), "external")
			if err := os.Symlink(external, filepath.Join(path, runLockFile)); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if _, err := os.Lstat(external); !os.IsNotExist(err) {
					t.Fatalf("external run lock target created: %v", err)
				}
			})
		}, "run lock is not a regular file"},
		{"live PID", func(_ *testing.T, _ string, deps *vmDeletionDeps) {
			deps.live = func(string) (bool, error) { return true, nil }
		}, "live runtime owner"},
		{"unknown PID", func(_ *testing.T, _ string, deps *vmDeletionDeps) {
			deps.live = func(string) (bool, error) { return false, errors.New("unavailable") }
		}, "observe vm deletion owner"},
		{"aux holder", func(_ *testing.T, _ string, deps *vmDeletionDeps) {
			deps.holders = func(path string) ([]int, error) {
				if filepath.Base(path) == "aux.img" {
					return []int{os.Getpid()}, nil
				}
				return nil, nil
			}
		}, "aux.img"},
		{"unknown file holders", func(_ *testing.T, _ string, deps *vmDeletionDeps) {
			deps.holders = func(string) ([]int, error) { return nil, errors.New("unavailable") }
		}, "observe vm file holders"},
		{"ambiguous symlink", func(t *testing.T, path string, _ *vmDeletionDeps) {
			if err := os.Symlink(filepath.Join(t.TempDir(), "external"), filepath.Join(path, "linked")); err != nil {
				t.Fatal(err)
			}
		}, "ambiguous symlink"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(vmconfig.StateDirEnv, t.TempDir())
			writeTreeVM(t, "protected", vmconfig.Config{})
			path := vmconfig.Path("protected")
			if err := os.WriteFile(filepath.Join(path, "aux.img"), []byte("retained"), 0600); err != nil {
				t.Fatal(err)
			}
			deps := vmDeletionDeps{live: func(string) (bool, error) { return false, nil }, holders: func(string) ([]int, error) { return nil, nil }}
			tt.setup(t, path, &deps)
			guard, err := mutationguard.Acquire(coveRoot())
			if err != nil {
				t.Fatal(err)
			}
			defer guard.Release()
			err = deleteVMWithOptionsGuarded(guard, "protected", DeleteVMOptions{}, deps)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("deletion error = %v, want %q", err, tt.want)
			}
			if _, err := os.Stat(filepath.Join(path, "aux.img")); err != nil {
				t.Fatalf("protected guest changed: %v", err)
			}
		})
	}
}

func TestVMDeletionFileHoldersSkipsOnlyOwnRunLock(t *testing.T) {
	path := t.TempDir()
	if err := os.WriteFile(filepath.Join(path, runLockFile), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkVMDeletionFileHolders(path, func(string) ([]int, error) { return []int{os.Getpid()}, nil }); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "disk.img"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkVMDeletionFileHolders(path, func(string) ([]int, error) { return []int{os.Getpid()}, nil }); err == nil {
		t.Fatal("own disk holder was ignored")
	}
}

func TestVMDeletionFileHolderInventoryBound(t *testing.T) {
	path := t.TempDir()
	deep := filepath.Join(path, strings.Repeat("deep/", 34))
	if err := os.MkdirAll(deep, 0700); err != nil {
		t.Fatal(err)
	}
	if err := checkVMDeletionFileHolders(path, func(string) ([]int, error) { return nil, nil }); err == nil || !strings.Contains(err.Error(), "bound") {
		t.Fatalf("inventory error = %v, want bound refusal", err)
	}
}

func TestVMDeletionCascadeRetainsPinnedParentAndChildren(t *testing.T) {
	t.Setenv(vmconfig.StateDirEnv, t.TempDir())
	writeTreeVM(t, "protected", vmconfig.Config{})
	writeTreeVM(t, "child", vmconfig.Config{ParentVM: "protected"})
	pins := storagepins.New()
	if err := pins.Add("vm", "protected", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := storagepins.Save(coveRoot(), pins); err != nil {
		t.Fatal(err)
	}
	if err := DeleteVMWithOptions("protected", DeleteVMOptions{Cascade: true}); err == nil {
		t.Fatal("cascade removed pinned parent")
	}
	for _, name := range []string{"protected", "child"} {
		if _, err := os.Stat(vmconfig.Path(name)); err != nil {
			t.Fatalf("guest %s removed: %v", name, err)
		}
	}
}

func TestVMDeletionCascadeRetainsPinnedChild(t *testing.T) {
	t.Setenv(vmconfig.StateDirEnv, t.TempDir())
	writeTreeVM(t, "parent", vmconfig.Config{})
	writeTreeVM(t, "protected", vmconfig.Config{ParentVM: "parent"})
	pins := storagepins.New()
	if err := pins.Add("vm", "protected", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := storagepins.Save(coveRoot(), pins); err != nil {
		t.Fatal(err)
	}
	guard, err := mutationguard.Acquire(coveRoot())
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Release()
	deps := vmDeletionDeps{live: func(string) (bool, error) { return false, nil }, holders: func(string) ([]int, error) { return nil, nil }}
	err = deleteVMWithOptionsGuarded(guard, "parent", DeleteVMOptions{Cascade: true}, deps)
	if err == nil || !strings.Contains(err.Error(), "pinned") {
		t.Fatalf("cascade error = %v, want pinned child refusal", err)
	}
	for _, name := range []string{"parent", "protected"} {
		if _, err := os.Stat(vmconfig.Path(name)); err != nil {
			t.Fatalf("guest %s removed: %v", name, err)
		}
	}
}

func TestVMRenameRetainsPinnedSourceAndTarget(t *testing.T) {
	for _, pinned := range []string{"source", "target"} {
		t.Run(pinned, func(t *testing.T) {
			t.Setenv(vmconfig.StateDirEnv, t.TempDir())
			writeTreeVM(t, "source", vmconfig.Config{})
			path := vmconfig.Path("source")
			if err := vmconfig.EnsurePackageAlias("source", path); err != nil {
				t.Fatal(err)
			}
			identity, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			alias, err := os.Readlink(vmconfig.PackageAliasPath("source"))
			if err != nil {
				t.Fatal(err)
			}
			pins := storagepins.New()
			if err := pins.Add("vm", pinned, time.Now()); err != nil {
				t.Fatal(err)
			}
			if err := storagepins.Save(coveRoot(), pins); err != nil {
				t.Fatal(err)
			}
			before := pins.List()
			if err := RenameVM("source", "target"); err == nil || !strings.Contains(err.Error(), "pinned") {
				t.Fatalf("rename error = %v, want pinned refusal", err)
			}
			current, err := os.Stat(path)
			if err != nil || !os.SameFile(identity, current) {
				t.Fatalf("source identity changed: %v", err)
			}
			currentAlias, err := os.Readlink(vmconfig.PackageAliasPath("source"))
			if err != nil || currentAlias != alias {
				t.Fatalf("source alias changed: %q, %v", currentAlias, err)
			}
			if _, err := os.Lstat(vmconfig.Path("target")); !os.IsNotExist(err) {
				t.Fatalf("target created: %v", err)
			}
			loaded, err := storagepins.Load(coveRoot())
			if err != nil || !reflect.DeepEqual(before, loaded.List()) {
				t.Fatalf("pins changed: %v", err)
			}
		})
	}
}
