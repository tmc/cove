package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tmc/cove/internal/disposable"
	"github.com/tmc/cove/internal/vmconfig"
)

var rollbackSnapshotName string

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
func SetupRollbackSnapshotClone(opts RollbackSnapshotCloneOptions) (disposable.Clone, error) {
	source := strings.TrimSpace(opts.Source)
	if source == "" {
		return disposable.Clone{}, fmt.Errorf("source vm is required")
	}
	if err := validateSnapshotName(opts.Snapshot); err != nil {
		return disposable.Clone{}, err
	}

	sourcePath := vmconfig.Path(source)
	if !vmconfig.Validate(sourcePath) {
		return disposable.Clone{}, fmt.Errorf("source vm not found: %s", source)
	}

	snapshotDiskPath := filepath.Join(sourcePath, "disk-snapshots", opts.Snapshot, "disk.img")
	if _, err := os.Stat(snapshotDiskPath); err != nil {
		if os.IsNotExist(err) {
			return disposable.Clone{}, fmt.Errorf("disk snapshot '%s' not found", opts.Snapshot)
		}
		return disposable.Clone{}, fmt.Errorf("stat snapshot disk: %w", err)
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
		return disposable.Clone{}, fmt.Errorf("create rollback clone: %w", err)
	}
	if err := os.Remove(proxyStatePath(vmconfig.Path(cloneName))); err != nil && !os.IsNotExist(err) {
		return disposable.Clone{}, fmt.Errorf("clear proxy state from rollback clone: %w", err)
	}

	return disposable.Clone{
		Name:      cloneName,
		Path:      vmconfig.Path(cloneName),
		Source:    source,
		CreatedAt: createdAt,
	}, nil
}
