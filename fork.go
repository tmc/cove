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
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"

	"github.com/tmc/cove/internal/vmconfig"
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
	if err := removeForkMACAddress(child); err != nil {
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

// ForkVMOptions configures a snapshot-aware fork. Snapshot is the name
// of an existing parent snapshot (vmDir/snapshots/<name>.vmstate); if
// empty, ForkVMWithSnapshot is equivalent to ForkVM.
type ForkVMOptions struct {
	Parent   string
	Child    string
	Snapshot string
}

// ForkVMWithSnapshot creates a child VM as a CoW fork of parent and,
// when Snapshot is non-empty, seeds the child's suspend.vmstate from
// the parent's saved snapshot at vmDir/snapshots/<name>.vmstate.
// When Snapshot is non-empty:
//
//   - The parent must be stopped: ForkVMWithSnapshot acquires the parent's
//     run.lock exclusively for the duration of the copy. Concurrent cove run
//     of the parent will fail until the fork completes.
//   - The parent's snapshots/<name>.vmstate must exist; create one with
//     "cove snapshot save <name>" while the parent is running first.
//   - The seeded suspend.vmstate is paired with the parent's aux.img
//     (copied byte-for-byte). On first child boot, VZ attempts a state
//     restore. In current Phase 2, the child's machine.id is rotated
//     by CloneVM, which causes VZ to reject the restore; the existing
//     suspend-restore fallback in macos.go then moves the seed aside
//     and the child cold-boots from the cloned disk. Reaching the
//     instant-resume path (design 013 Model A1) requires a future
//     identity-preserving fork option that keeps the parent's
//     machine.id alongside the seeded vmstate.
//
// When Snapshot is empty, this defers to ForkVM and inherits its
// best-effort semantics against a running parent (no lock acquired).
func ForkVMWithSnapshot(opts ForkVMOptions) error {
	if opts.Snapshot == "" {
		return ForkVM(opts.Parent, opts.Child)
	}
	if opts.Parent == "" {
		return errors.New("fork: parent VM name required")
	}
	if opts.Child == "" {
		return errors.New("fork: child VM name required")
	}
	if opts.Parent == opts.Child {
		return errors.New("fork: parent and child must differ")
	}
	if err := validateSnapshotName(opts.Snapshot); err != nil {
		return fmt.Errorf("fork: %w", err)
	}
	parentDir := vmconfig.Path(opts.Parent)
	if !vmconfig.Validate(parentDir) {
		return fmt.Errorf("fork: parent VM not found: %s", opts.Parent)
	}
	snapshotPath := filepath.Join(parentDir, "snapshots", opts.Snapshot+".vmstate")
	if _, err := os.Stat(snapshotPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("fork: snapshot %q not found on parent %q (expected at %s); create one first with: cove snapshot save %s (parent VM must be running)", opts.Snapshot, opts.Parent, snapshotPath, opts.Snapshot)
		}
		return fmt.Errorf("fork: stat snapshot: %w", err)
	}

	// Hold parent's run.lock exclusively for the duration of the copy.
	// Phase 0 invariant: a running parent holds this lock; if the
	// acquire fails with ErrRunLockHeld, the parent is running and a
	// snapshot-seeded fork would race with parent writes to aux.img.
	lock, err := acquireRunLockHook(parentDir)
	if err != nil {
		if errors.Is(err, ErrRunLockHeld) {
			return fmt.Errorf("fork: parent VM %q is running; -snapshot fork requires parent stopped (or use plain 'cove fork' for best-effort)", opts.Parent)
		}
		return fmt.Errorf("fork: acquire parent run.lock: %w", err)
	}
	defer func() {
		if releaseErr := lock.Release(); releaseErr != nil {
			fmt.Fprintf(os.Stderr, "warning: release parent run.lock: %v\n", releaseErr)
		}
	}()

	// CloneVM copies aux.img + hw.model + clonefile's disk.img and
	// removes any leftover suspend.vmstate from the child for
	// deterministic cold boot. We re-seed suspend.vmstate from the
	// parent's snapshot afterwards.
	if err := CloneVM(CloneOptions{
		Source:        opts.Parent,
		Target:        opts.Child,
		Linked:        true,
		CopyMachineID: false,
	}); err != nil {
		return err
	}
	childDir := vmconfig.Path(opts.Child)
	if err := removeForkMACAddress(opts.Child); err != nil {
		os.RemoveAll(childDir)
		return err
	}
	if err := copyFile(snapshotPath, filepath.Join(childDir, "suspend.vmstate")); err != nil {
		// Roll back the partial clone so we don't leave a half-forked VM.
		os.RemoveAll(childDir)
		return fmt.Errorf("fork: seed suspend.vmstate from snapshot %q: %w", opts.Snapshot, err)
	}
	if err := recordForkLineage(opts.Parent, opts.Child, opts.Snapshot, time.Now().UTC()); err != nil {
		return fmt.Errorf("fork: record lineage: %w", err)
	}
	return nil
}

func removeForkMACAddress(name string) error {
	path := filepath.Join(vmconfig.Path(name), "mac.address")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("fork: remove mac address: %w", err)
	}
	return nil
}
