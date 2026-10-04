package checkpoint

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

// AcquireRead protects a clone or export against checkpoint storage changes.
// Acquire VM ownership first when required, then this operation lock. The lock
// is nonblocking and does not replace runtime ownership or quiescence checks.
func AcquireRead(root string) (func() error, error) {
	release, err := lockOperation(root, false)
	if err != nil {
		return nil, err
	}
	pending, err := New(root).Pending()
	if err != nil || pending {
		release()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("checkpoint restore recovery is required before reading VM storage")
	}
	return release, nil
}

func lockOperation(root string, exclusive bool) (func() error, error) {
	if err := realDirectory(root); err != nil {
		return nil, err
	}
	fd, err := unix.Open(filepath.Join(root, ".checkpoint.operations.lock"), unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "checkpoint operations lock")
	mode := unix.LOCK_SH
	if exclusive {
		mode = unix.LOCK_EX
	}
	if err := unix.Flock(fd, mode|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("checkpoint storage operation is in progress; retry after it completes: %w", err)
	}
	var once sync.Once
	var closeErr error
	return func() error { once.Do(func() { closeErr = f.Close() }); return closeErr }, nil
}

// AcquireExclusive protects stopped storage removal against checkpoint reads
// and writes. Acquire runtime ownership first. Pending recovery is refused.
func AcquireExclusive(root string) (func() error, error) {
	release, err := lockOperation(root, true)
	if err != nil {
		return nil, err
	}
	pending, err := New(root).Pending()
	if err != nil || pending {
		release()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("checkpoint restore recovery is required before removing VM storage")
	}
	return release, nil
}
