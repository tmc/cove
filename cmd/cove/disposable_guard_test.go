package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tmc/cove/internal/disposable"
	"github.com/tmc/cove/internal/mutationguard"
	"github.com/tmc/cove/internal/storagepins"
	"github.com/tmc/cove/internal/vmconfig"
)

func TestDisposableCleanupMutationGuard(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := t.TempDir()
	guard, err := mutationguard.Acquire(coveRoot())
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Release()
	if err := CleanupDisposableClone(path); !errors.Is(err, mutationguard.ErrBusy) {
		t.Fatalf("cleanup error = %v, want mutation guard busy", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("clone was removed: %v", err)
	}
}

func TestDisposableCleanupRefusesRunLock(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := vmconfig.Path("clone")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	lock, err := AcquireRunLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	if err := CleanupDisposableClone(path); !errors.Is(err, ErrRunLockHeld) {
		t.Fatalf("cleanup error = %v, want run lock held", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("clone was removed: %v", err)
	}
}

func TestDisposableGCGuardsDiscoveryAndRemoval(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	base := vmconfig.BaseDir()
	path := filepath.Join(base, disposableCloneName("base", time.Now().Add(-time.Hour)))
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	assertGuardHeld := func() {
		t.Helper()
		guard, err := mutationguard.Acquire(coveRoot())
		if guard != nil {
			guard.Release()
		}
		if !errors.Is(err, mutationguard.ErrBusy) {
			t.Fatalf("guard error = %v, want busy", err)
		}
	}
	result, err := GCDisposableClones(disposable.GCOptions{
		BaseDir: base,
		IsActive: func(string) bool {
			assertGuardHeld()
			return false
		},
		RemoveAll: func(path string) error {
			assertGuardHeld()
			lock, err := AcquireRunLock(path)
			if lock != nil {
				lock.Release()
			}
			if !errors.Is(err, ErrRunLockHeld) {
				t.Fatalf("removal run lock error = %v, want held", err)
			}
			return os.RemoveAll(path)
		},
	})
	if err != nil || result.Removed != 1 {
		t.Fatalf("gc = %#v, %v, want one removal", result, err)
	}
}

func TestDisposableCleanupRetainsOperatorPin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	name := disposableCloneName("pinned", time.Now().Add(-time.Hour))
	path := vmconfig.Path(name)
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	pins := storagepins.New()
	if err := pins.Add("vm", name, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := storagepins.Save(coveRoot(), pins); err != nil {
		t.Fatal(err)
	}
	if err := CleanupDisposableClone(path); err == nil {
		t.Fatal("pinned clone cleanup succeeded")
	}
	legacy := filepath.Join(vmconfig.BaseDir(), name)
	if err := os.Symlink(path, legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := GCDisposableClones(disposable.GCOptions{BaseDir: vmconfig.BaseDir()}); err == nil {
		t.Fatal("gc removed pinned clone")
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatal(err)
	}
	loaded, err := storagepins.Load(coveRoot())
	if err != nil || !loaded.IsPinned("vm", name) {
		t.Fatalf("pin not retained: %v", err)
	}
}

func TestDisposableGCPackageAndAliasRemovedOnce(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	name := disposableCloneName("old", time.Now().Add(-time.Hour))
	path := vmconfig.Path(name)
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, filepath.Join(vmconfig.BaseDir(), name)); err != nil {
		t.Fatal(err)
	}
	calls := 0
	result, err := GCDisposableClones(disposable.GCOptions{
		BaseDir: vmconfig.BaseDir(),
		RemoveAll: func(target string) error {
			calls++
			return os.RemoveAll(target)
		},
	})
	if err != nil || calls != 1 || result.Removed != 1 || result.Scanned != 1 {
		t.Fatalf("gc = %#v, calls = %d, error = %v, want one removal", result, calls, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("clone was retained: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(vmconfig.BaseDir(), name)); !os.IsNotExist(err) {
		t.Fatalf("compatibility alias was retained: %v", err)
	}
}

func TestDisposableCleanupRejectsUnregisteredPath(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := t.TempDir()
	if err := CleanupDisposableClone(path); !errors.Is(err, ErrDisposableUnsafePath) {
		t.Fatalf("cleanup error = %v, want unsafe path", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestImageMaterializationMutationGuard(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	guard, err := mutationguard.Acquire(coveRoot())
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Release()
	if _, err := MaterializeImage(MaterializeImageOptions{}); !errors.Is(err, mutationguard.ErrBusy) {
		t.Fatalf("materialization error = %v, want mutation guard busy", err)
	}
	if _, err := materializeImageGuarded(nil, MaterializeImageOptions{}); err == nil {
		t.Fatal("materialization with nil guard succeeded")
	}
}
