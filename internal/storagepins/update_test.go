package storagepins

import (
	"errors"
	"testing"
	"time"

	"github.com/tmc/cove/internal/mutationguard"
)

func TestUpdatePreservesPinsAndFailedChanges(t *testing.T) {
	root := t.TempDir()
	add := func(id string) error {
		return Update(root, func(f *File) (bool, error) { return true, f.Add("vm", id, time.Now()) })
	}
	if err := add("first"); err != nil {
		t.Fatal(err)
	}
	if err := add("second"); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("callback failed")
	if err := Update(root, func(f *File) (bool, error) { f.Remove("vm", "first"); return true, failure }); !errors.Is(err, failure) {
		t.Fatalf("error %v", err)
	}
	f, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !f.IsPinned("vm", "first") || !f.IsPinned("vm", "second") {
		t.Fatalf("pins %v", f.List())
	}
}

func TestUpdateAndSaveRespectMutationGuard(t *testing.T) {
	root := t.TempDir()
	guard, err := mutationguard.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Release()
	called := false
	if err := Update(root, func(*File) (bool, error) { called = true; return true, nil }); !errors.Is(err, mutationguard.ErrBusy) {
		t.Fatalf("update error %v", err)
	}
	if called {
		t.Fatal("update callback ran without guard")
	}
	if err := Save(root, New()); !errors.Is(err, mutationguard.ErrBusy) {
		t.Fatalf("save error %v", err)
	}
}
