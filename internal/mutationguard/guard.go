// Package mutationguard excludes concurrent mutations of a cove storage root.
// Acquire the root guard before VM run locks and checkpoint operation locks.
package mutationguard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// ErrBusy indicates that another operation holds the root mutation guard.
var ErrBusy = unix.EWOULDBLOCK

// Guard holds the root's mutation lock until Release. Its zero value is usable.
type Guard struct {
	mu   sync.Mutex
	file *os.File
	root *os.Root
}

// Acquire takes the storage root's exclusive mutation guard without waiting.
// A busy root returns ErrBusy, including when the caller holds another guard.
func Acquire(root string) (*Guard, error) {
	if root == "" {
		return nil, fmt.Errorf("mutation guard root required")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return nil, fmt.Errorf("create mutation guard root: %w", err)
	}
	dir, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open mutation guard root: %w", err)
	}
	parent, err := dir.Open(".")
	if err != nil {
		dir.Close()
		return nil, err
	}
	fd, err := unix.Openat(int(parent.Fd()), "mutation.lock", unix.O_CREAT|unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	parent.Close()
	if err != nil {
		dir.Close()
		return nil, fmt.Errorf("open mutation guard: %w", err)
	}
	file := os.NewFile(uintptr(fd), "mutation.lock")
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		dir.Close()
		return nil, fmt.Errorf("mutation guard is not a regular file")
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		dir.Close()
		return nil, fmt.Errorf("acquire mutation guard: %w", err)
	}
	return &Guard{file: file, root: dir}, nil
}

// Release drops the guard. It is safe to call more than once.
func (g *Guard) Release() error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.file == nil {
		return nil
	}
	err := errors.Join(g.file.Close(), g.root.Close())
	g.file = nil
	g.root = nil
	return err
}

// Check reports whether g still holds the guard for root.
func (g *Guard) Check(root string) error {
	if g == nil {
		return fmt.Errorf("mutation guard required")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.file == nil || g.root == nil {
		return fmt.Errorf("mutation guard is released")
	}
	held, err := g.root.Stat(".")
	if err != nil {
		return err
	}
	current, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !os.SameFile(held, current) {
		return fmt.Errorf("mutation guard root differs")
	}
	return nil
}

// AcquireContext waits for the root guard until ctx ends.
// Callers must acquire it before VM run locks or checkpoint locks.
func AcquireContext(ctx context.Context, root string) (*Guard, error) {
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(err, lastErr)
		}
		guard, err := Acquire(root)
		if !errors.Is(err, ErrBusy) && !errors.Is(err, os.ErrNotExist) {
			return guard, err
		}
		lastErr = err
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, errors.Join(ctx.Err(), lastErr)
		case <-timer.C:
		}
	}
}
