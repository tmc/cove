// run_lock.go — exclusive run-time lock on a VM directory.
//
// Each running cove VM holds an advisory exclusive flock on
// <vmDir>/run.lock for the lifetime of the process. The kernel
// releases the lock when the file descriptor closes — including on
// crash — so no manual cleanup is needed for stale lock files. See
// docs/designs/013-vm-fork.md "Concurrent-run guard" for why this is
// the precondition for snapshot-seeded forking and ephemeral siblings.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

const runLockFile = "run.lock"

// RunLock holds the open file descriptor and flock on a VM directory's
// run.lock file. Release closes the descriptor, which drops the lock.
type RunLock struct {
	f *os.File
}

// AcquireRunLock takes an exclusive non-blocking flock on
// <vmDir>/run.lock. The directory must already exist; the lock file
// is created if missing. Returns ErrRunLockHeld if another process
// holds the lock; the returned error message names the holding PID
// when discoverable.
func AcquireRunLock(vmDir string) (*RunLock, error) {
	if vmDir == "" {
		return nil, errors.New("run.lock: vmDir required")
	}
	if info, err := os.Stat(vmDir); err != nil {
		return nil, fmt.Errorf("run.lock: stat vmDir: %w", err)
	} else if !info.IsDir() {
		return nil, fmt.Errorf("run.lock: vmDir is not a directory: %s", vmDir)
	}
	path := filepath.Join(vmDir, runLockFile)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("run.lock: open: %w", err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			holder := runLockHolderHint(path)
			return nil, fmt.Errorf("run.lock: %s already held%s: %w", path, holder, err)
		}
		return nil, fmt.Errorf("run.lock: flock: %w", err)
	}
	return &RunLock{f: f}, nil
}

// Release drops the flock by closing the descriptor. Safe to call
// multiple times; subsequent calls are no-ops.
func (l *RunLock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// ErrRunLockHeld is returned by callers that need to discriminate the
// "another process holds the lock" case from generic I/O failure. The
// returned error from AcquireRunLock wraps EWOULDBLOCK; use
// errors.Is(err, ErrRunLockHeld) to match.
var ErrRunLockHeld = unix.EWOULDBLOCK

// runLockHolderHint returns " (recover with: lsof <path>)" so the
// operator has a recipe when the lock is contested. The hint is
// best-effort and never returns an error — a missing lsof is fine.
func runLockHolderHint(path string) string {
	return fmt.Sprintf(" (recover with: lsof %s)", path)
}
