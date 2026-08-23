package main

import (
	"errors"
	"os"
	"testing"

	"github.com/tmc/cove/internal/vmrun"
)

func TestCheckSuspendConfigMatchMissingFileReturnsNil(t *testing.T) {
	hc := vmrun.HostConfig{VMDir: t.TempDir()}
	if err := checkSuspendConfigMatchForRun(vmrun.RunConfig{}, hc); err != nil {
		t.Fatalf("err = %v, want nil for missing config", err)
	}
}

// Discarding a suspend must take the fingerprint with it: a fingerprint left
// behind describes a state file that no longer exists.
func TestDiscardSuspendStateRemovesBothFiles(t *testing.T) {
	dir := t.TempDir()
	for _, path := range []string{suspendStatePathForVM(dir), suspendConfigPathForVM(dir)} {
		if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
			t.Fatalf("WriteFile %s: %v", path, err)
		}
	}
	discardSuspendStateForVM(dir)
	for _, path := range []string{suspendStatePathForVM(dir), suspendConfigPathForVM(dir)} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Stat(%s) = %v, want ErrNotExist", path, err)
		}
	}
	// Removing an already-absent suspend is not an error.
	discardSuspendStateForVM(dir)
}

// A corrupt fingerprint is not evidence that the device topology matches, so the
// guard must report it rather than waving the restore through.
func TestCheckSuspendConfigMatchCorruptFileReturnsError(t *testing.T) {
	hc := vmrun.HostConfig{VMDir: t.TempDir()}
	if err := os.WriteFile(suspendConfigPathForVM(hc.VMDir), []byte("not-json"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := checkSuspendConfigMatchForRun(vmrun.RunConfig{}, hc); err == nil {
		t.Fatal("err = nil, want error for corrupt config")
	}
}

// An unreadable fingerprint must not be classified as "no saved config": only
// os.ErrNotExist means there is nothing to compare against.
func TestCheckSuspendConfigMatchUnreadableFileReturnsError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses file permissions")
	}
	hc := vmrun.HostConfig{VMDir: t.TempDir()}
	path := suspendConfigPathForVM(hc.VMDir)
	if err := os.WriteFile(path, []byte("{}"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	err := checkSuspendConfigMatchForRun(vmrun.RunConfig{}, hc)
	if err == nil {
		t.Fatal("err = nil, want error for unreadable config")
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err = %v, want a read error not ErrNotExist", err)
	}
}
