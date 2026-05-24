//go:build darwin || linux || freebsd || netbsd || openbsd || dragonfly

package imagestore

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

const lockFile = ".image.lock"

func acquireLock(imageDir string, nonblock bool) (*Lock, error) {
	if err := checkLockDir(imageDir); err != nil {
		return nil, err
	}
	path := filepath.Join(imageDir, lockFile)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("image.lock: open: %w", err)
	}
	how := unix.LOCK_EX
	if nonblock {
		how |= unix.LOCK_NB
	}
	if err := unix.Flock(int(f.Fd()), how); err != nil {
		f.Close()
		return nil, fmt.Errorf("image.lock: flock %s: %w", path, err)
	}
	return &Lock{release: f.Close}, nil
}
