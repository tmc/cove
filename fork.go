// fork.go — VM fork primitives.
//
// Forking creates a new disk image that shares blocks with its parent via
// APFS clonefile (copy-on-write). Writes to either side allocate fresh
// blocks; the other side is unaffected. See docs/designs/013-vm-fork.md
// for the full model. This file holds the lowest-level building block —
// a single-file CoW clone — that higher-level fork operations
// (cove fork, disk-snapshot restore, disposable run) compose.
package main

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// ForkVMDisk creates child as a copy-on-write clone of parent using APFS
// clonefile. The two files share blocks until either is written; subsequent
// writes diverge per file. parent is unchanged. child must not exist.
//
// Returns an error if parent does not exist, child already exists, or the
// underlying filesystem does not support clonefile (e.g. not APFS). No
// fallback to byte-copy is performed: callers that want a copy fallback
// should handle it explicitly so they know whether they paid the CoW cost.
func ForkVMDisk(parent, child string) error {
	if parent == "" {
		return errors.New("fork: parent path required")
	}
	if child == "" {
		return errors.New("fork: child path required")
	}
	if _, err := os.Stat(parent); err != nil {
		return fmt.Errorf("fork: stat parent: %w", err)
	}
	if _, err := os.Stat(child); err == nil {
		return fmt.Errorf("fork: child already exists: %s", child)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("fork: stat child: %w", err)
	}
	if err := unix.Clonefile(parent, child, 0); err != nil {
		return fmt.Errorf("fork: clonefile %s -> %s: %w", parent, child, err)
	}
	return nil
}
