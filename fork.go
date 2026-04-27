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
	"time"

	"golang.org/x/sys/unix"

	"github.com/tmc/vz-macos/internal/vmconfig"
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

// ForkVM creates a child VM as a CoW fork of parent. The child gets:
//   - disk.img cloned via APFS clonefile (or full copy if filesystem
//     does not support clonefile and linked is false)
//   - aux.img and hw.model byte-copied from parent (required for VZ
//     hardware-model identity match)
//   - a fresh machine.id (unique platform identifier)
//   - no copied mac.address (a fresh MAC is allocated on first boot)
//   - no copied suspend.vmstate (deterministic cold boot)
//
// parent must exist and child must not. The child's config records lineage
// metadata so cove vm tree and future GC policy can trace fork ancestry.
//
// This is a thin convenience wrapper over CloneVM that hard-codes
// fork semantics: linked-by-default (CoW) and never copy machine.id.
func ForkVM(parent, child string) error {
	if parent == "" {
		return errors.New("fork: parent VM name required")
	}
	if child == "" {
		return errors.New("fork: child VM name required")
	}
	if parent == child {
		return errors.New("fork: parent and child must differ")
	}
	if !vmconfig.Validate(vmconfig.Path(parent)) {
		return fmt.Errorf("fork: parent VM not found: %s", parent)
	}
	if err := CloneVM(CloneOptions{
		Source:        parent,
		Target:        child,
		Linked:        true,
		CopyMachineID: false,
	}); err != nil {
		return err
	}
	if err := recordForkLineage(parent, child, "", time.Now().UTC()); err != nil {
		return fmt.Errorf("fork: record lineage: %w", err)
	}
	return nil
}

func recordForkLineage(parent, child, snapshot string, forkedAt time.Time) error {
	dir := vmconfig.Path(child)
	cfg, err := vmconfig.Load(dir)
	if err != nil {
		return err
	}
	cfg.ParentVM = parent
	cfg.ParentSnapshot = snapshot
	cfg.ForkedAt = forkedAt
	return vmconfig.Save(dir, cfg)
}
