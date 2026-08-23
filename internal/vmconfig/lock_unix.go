//go:build darwin || linux || freebsd || netbsd || openbsd || dragonfly

package vmconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// acquireConfigLock takes an exclusive flock on <dir>/.config.lock,
// retrying until configLockTimeout elapses. The returned release
// function closes the descriptor, which drops the lock — including if
// the holding process dies, so an abandoned lock never persists.
func acquireConfigLock(dir string) (func(), error) {
	if dir == "" {
		return nil, errors.New("vmconfig: lock dir required")
	}
	path := filepath.Join(dir, configLockFile)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("open vm config lock: %w", err)
	}
	deadline := time.Now().Add(configLockTimeout)
	backoff := time.Millisecond
	for {
		err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() { f.Close() }, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) || time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("lock vm config: %w", err)
		}
		time.Sleep(backoff)
		if backoff < 20*time.Millisecond {
			backoff *= 2
		}
	}
}
