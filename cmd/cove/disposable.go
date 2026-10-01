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
	"github.com/tmc/cove/internal/mutationguard"
	"github.com/tmc/cove/internal/storagepins"
	"github.com/tmc/cove/internal/vmconfig"
)

const disposableCloneStampFormat = disposable.CloneStampFormat

// DisposableSetupOptions configures disposable clone creation.
type DisposableSetupOptions struct {
	Source         string
	Target         string
	Linked         bool
	CopyMachineID  bool
	SourceDiskPath string
	Now            func() time.Time
	Clone          func(CloneOptions) error
}

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
func SetupDisposableClone(opts DisposableSetupOptions) (disposable.Clone, error) {
	if strings.TrimSpace(opts.Source) == "" {
		return disposable.Clone{}, fmt.Errorf("source vm is required")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	createdAt := now()
	cloneName := strings.TrimSpace(opts.Target)
	if cloneName == "" {
		cloneName = disposableCloneName(opts.Source, createdAt)
	}
	if cloneName == opts.Source {
		return disposable.Clone{}, fmt.Errorf("target vm must differ from source")
	}
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
		return disposable.Clone{}, fmt.Errorf("create disposable clone: %w", err)
	}
	if err := os.Remove(proxyStatePath(vmconfig.Path(cloneName))); err != nil && !os.IsNotExist(err) {
		return disposable.Clone{}, fmt.Errorf("clear proxy state from disposable clone: %w", err)
	}
	return disposable.Clone{
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
	guard, err := mutationguard.Acquire(coveRoot())
	if err != nil {
		return fmt.Errorf("guard disposable cleanup: %w", err)
	}
	defer guard.Release()
	return cleanupDisposableCloneGuarded(guard, clean, os.RemoveAll)
}

func cleanupDisposableCloneGuarded(guard *mutationguard.Guard, path string, remove func(string) error) error {
	if err := guard.Check(coveRoot()); err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	root, err := filepath.EvalSymlinks(coveRoot())
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, canonical)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: outside storage root", ErrDisposableUnsafePath)
	}
	name := vmconfig.NameForPath(path)
	registered, err := filepath.EvalSymlinks(vmconfig.Path(name))
	if err != nil || registered != canonical {
		return fmt.Errorf("%w: vm registration differs", ErrDisposableUnsafePath)
	}
	pins, err := storagepins.Load(coveRoot())
	if err != nil {
		return fmt.Errorf("load disposable cleanup pins: %w", err)
	}
	if pins.IsPinned("vm", name) {
		return fmt.Errorf("disposable clone is pinned; retained")
	}
	alias := filepath.Join(vmconfig.BaseDir(), name)
	removeAlias := false
	if info, err := os.Lstat(alias); err == nil && info.Mode()&os.ModeSymlink != 0 {
		target, err := filepath.EvalSymlinks(alias)
		removeAlias = err == nil && target == canonical
	}
	path = canonical
	lock, err := AcquireRunLock(path)
	if err != nil {
		return fmt.Errorf("lock disposable cleanup: %w", err)
	}
	defer lock.Release()
	if disposableCloneHasControlSocket(path) {
		return fmt.Errorf("disposable clone is active; retained")
	}
	if err := remove(path); err != nil {
		return err
	}
	if removeAlias {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err := os.Remove(alias); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove disposable compatibility alias: %w", err)
			}
		}
	}
	return nil
}

// GCDisposableClones removes disposable clones older than OlderThan.
func GCDisposableClones(opts disposable.GCOptions) (disposable.GCResult, error) {
	guard, err := mutationguard.Acquire(coveRoot())
	if err != nil {
		return disposable.GCResult{}, fmt.Errorf("guard disposable gc: %w", err)
	}
	defer guard.Release()
	if opts.BaseDir == "" {
		opts.BaseDir = vmconfig.BaseDir()
	}
	if opts.IsActive == nil {
		opts.IsActive = disposableCloneIsActive
	}
	remove := opts.RemoveAll
	if remove == nil {
		remove = os.RemoveAll
	}
	opts.RemoveAll = func(path string) error {
		return cleanupDisposableCloneGuarded(guard, path, remove)
	}
	return disposable.GC(opts)
}

func disposableCloneIsActive(path string) bool {
	if isVMRunningAt(path) {
		return true
	}
	return disposableCloneHasControlSocket(path)
}

func disposableCloneHasControlSocket(path string) bool {
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
