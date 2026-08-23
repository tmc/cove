//go:build !(darwin || linux || freebsd || netbsd || openbsd || dragonfly)

package vmconfig

import "errors"

// acquireConfigLock reports that advisory locking is unavailable.
// Callers fall back to unlocked, still-atomic writes.
func acquireConfigLock(dir string) (func(), error) {
	return nil, errors.New("vmconfig: config lock unsupported on this platform")
}
