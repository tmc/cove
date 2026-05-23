package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tmc/cove/internal/disposable"
	"github.com/tmc/cove/internal/vmconfig"
)

const disposableCloneStampFormat = disposable.CloneStampFormat

// DisposableClone describes a disposable VM clone created for a session.
type DisposableClone = disposable.Clone

// DisposableSetupOptions configures disposable clone creation.
type DisposableSetupOptions struct {
	Source         string
	Linked         bool
	CopyMachineID  bool
	SourceDiskPath string
	Now            func() time.Time
	Clone          func(CloneOptions) error
}

// DisposableGCOptions configures disposable VM garbage collection.
type DisposableGCOptions = disposable.GCOptions

// DisposableGCResult summarizes a garbage-collection run.
type DisposableGCResult = disposable.GCResult

// disposableCloneName returns a human-readable disposable VM name.
func disposableCloneName(base string, now time.Time) string {
	return disposable.CloneName(base, now)
}

// parseDisposableCloneName parses a disposable VM name produced by
// disposableCloneName.
func parseDisposableCloneName(name string) (base string, createdAt time.Time, ok bool) {
	return disposable.ParseCloneName(name)
}

// SetupDisposableClone creates a disposable VM clone from source.
func SetupDisposableClone(opts DisposableSetupOptions) (DisposableClone, error) {
	if strings.TrimSpace(opts.Source) == "" {
		return DisposableClone{}, fmt.Errorf("source vm is required")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	createdAt := now()
	cloneName := disposableCloneName(opts.Source, createdAt)
	cloneFn := opts.Clone
	if cloneFn == nil {
		cloneFn = CloneVM
	}
	cloneOpts := CloneOptions{
		Source:         opts.Source,
		Target:         cloneName,
		Linked:         opts.Linked,
		CopyMachineID:  opts.CopyMachineID,
		SourceDiskPath: opts.SourceDiskPath,
	}
	if err := cloneFn(cloneOpts); err != nil {
		return DisposableClone{}, fmt.Errorf("create disposable clone: %w", err)
	}
	if err := os.Remove(proxyStatePath(vmconfig.Path(cloneName))); err != nil && !os.IsNotExist(err) {
		return DisposableClone{}, fmt.Errorf("clear proxy state from disposable clone: %w", err)
	}
	return DisposableClone{
		Name:      cloneName,
		Path:      vmconfig.Path(cloneName),
		Source:    opts.Source,
		CreatedAt: createdAt,
	}, nil
}

// ErrDisposableUnsafePath is returned by CleanupDisposableClone when
// the supplied path is empty, ".", or a filesystem root. Callers can
// branch on this with errors.Is to distinguish a programmer mistake
// (refused destructive call) from an actual rm failure.
var ErrDisposableUnsafePath = errors.New("disposable clone path unsafe")

// CleanupDisposableClone removes a disposable clone directory.
func CleanupDisposableClone(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("%w: empty", ErrDisposableUnsafePath)
	}
	clean := filepath.Clean(path)
	if clean == "." || clean == string(filepath.Separator) {
		return fmt.Errorf("%w: %q", ErrDisposableUnsafePath, path)
	}
	return os.RemoveAll(path)
}

// GCDisposableClones removes disposable clones older than OlderThan.
func GCDisposableClones(opts DisposableGCOptions) (DisposableGCResult, error) {
	if opts.BaseDir == "" {
		opts.BaseDir = vmconfig.BaseDir()
	}
	if opts.IsActive == nil {
		opts.IsActive = disposableCloneIsActive
	}
	return disposable.GC(opts)
}

func disposableBaseName(base string) string {
	return disposable.BaseName(base)
}

func disposableCloneIsActive(path string) bool {
	if isVMRunningAt(path) {
		return true
	}
	sock := GetControlSocketPathForVM(path)
	if sock == "" {
		return false
	}
	conn, err := netDialUnixTimeout(sock, 200*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// netDialUnixTimeout is a small indirection so tests can stub it if needed.
var netDialUnixTimeout = func(sock string, timeout time.Duration) (net.Conn, error) {
	conn, err := net.DialTimeout("unix", sock, timeout)
	if err != nil {
		return nil, err
	}
	return conn, nil
}
