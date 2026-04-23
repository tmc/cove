package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var (
	rollbackSnapshotName           string
	setupRollbackSnapshotCloneHook = SetupRollbackSnapshotClone
)

// RollbackSnapshotCloneOptions configures creation of a disposable clone whose
// system disk comes from a saved disk snapshot.
type RollbackSnapshotCloneOptions struct {
	Source   string
	Snapshot string
	Now      func() time.Time
	Clone    func(CloneOptions) error
}

// SetupRollbackSnapshotClone creates a disposable VM clone whose disk is
// sourced from the named disk snapshot rather than the live VM disk.
func SetupRollbackSnapshotClone(opts RollbackSnapshotCloneOptions) (DisposableClone, error) {
	source := strings.TrimSpace(opts.Source)
	if source == "" {
		return DisposableClone{}, fmt.Errorf("source vm is required")
	}
	if err := validateSnapshotName(opts.Snapshot); err != nil {
		return DisposableClone{}, err
	}

	sourcePath := GetVMPath(source)
	if !ValidateVM(sourcePath) {
		return DisposableClone{}, fmt.Errorf("source vm not found: %s", source)
	}

	snapshotDiskPath := filepath.Join(sourcePath, "disk-snapshots", opts.Snapshot, "disk.img")
	if _, err := os.Stat(snapshotDiskPath); err != nil {
		if os.IsNotExist(err) {
			return DisposableClone{}, fmt.Errorf("disk snapshot '%s' not found", opts.Snapshot)
		}
		return DisposableClone{}, fmt.Errorf("stat snapshot disk: %w", err)
	}

	now := opts.Now
	if now == nil {
		now = time.Now
	}
	createdAt := now()
	cloneName := disposableCloneName(source, createdAt)
	cloneFn := opts.Clone
	if cloneFn == nil {
		cloneFn = CloneVM
	}

	cloneOpts := CloneOptions{
		Source:         source,
		Target:         cloneName,
		Linked:         true,
		CopyMachineID:  false,
		SourceDiskPath: snapshotDiskPath,
	}
	if err := cloneFn(cloneOpts); err != nil {
		return DisposableClone{}, fmt.Errorf("create rollback clone: %w", err)
	}
	if err := os.Remove(proxyStatePath(GetVMPath(cloneName))); err != nil && !os.IsNotExist(err) {
		return DisposableClone{}, fmt.Errorf("clear proxy state from rollback clone: %w", err)
	}

	return DisposableClone{
		Name:      cloneName,
		Path:      GetVMPath(cloneName),
		Source:    source,
		CreatedAt: createdAt,
	}, nil
}
