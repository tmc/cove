package main

import (
	"strings"
	"testing"
)

// TestHandleVMSharedFolderCommandEarlyBranches covers the dispatch branches
// that return before sharedFolderCommandVMDir is consulted: empty args,
// the three help aliases, and the "pending" too-many-args usage error.
func TestHandleVMSharedFolderCommandEarlyBranches(t *testing.T) {
	t.Run("empty args returns command-required", func(t *testing.T) {
		err := handleVMSharedFolderCommand(nil)
		if err == nil || !strings.Contains(err.Error(), "command required") {
			t.Fatalf("err = %v, want 'command required'", err)
		}
	})

	for _, alias := range []string{"help", "-h", "--help"} {
		t.Run("help alias "+alias, func(t *testing.T) {
			if err := handleVMSharedFolderCommand([]string{alias}); err != nil {
				t.Fatalf("help alias %q: %v, want nil", alias, err)
			}
		})
	}

	t.Run("pending too many args", func(t *testing.T) {
		oldVMDir, oldVMName := vmDir, vmName
		t.Cleanup(func() { vmDir, vmName = oldVMDir, oldVMName })
		vmDir = t.TempDir()
		vmName = "p"
		t.Setenv("HOME", t.TempDir())
		err := handleVMSharedFolderCommand([]string{"pending", "a", "b"})
		if err == nil || !strings.Contains(err.Error(), "usage: cove shared-folder pending") {
			t.Fatalf("err = %v, want pending usage error", err)
		}
	})

	t.Run("remove missing arg", func(t *testing.T) {
		oldVMDir, oldVMName := vmDir, vmName
		t.Cleanup(func() { vmDir, vmName = oldVMDir, oldVMName })
		vmDir = t.TempDir()
		vmName = "r"
		t.Setenv("HOME", t.TempDir())
		err := handleVMSharedFolderCommand([]string{"remove"})
		if err == nil || !strings.Contains(err.Error(), "usage: cove shared-folder remove") {
			t.Fatalf("err = %v, want remove usage error", err)
		}
	})
}
