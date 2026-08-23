package vmconfig

import "time"

// configLockFile is the advisory lock file guarding read-modify-write
// updates of config.json inside a VM directory.
const configLockFile = ".config.lock"

// configLockTimeout bounds how long a caller waits for the config lock.
// The lock is advisory: a stale or abandoned lock must never wedge the
// CLI, so on timeout the update proceeds unlocked. The write itself is
// still atomic (unique temp file plus rename), so the worst case is a
// lost concurrent field update, not a corrupt config.json.
var configLockTimeout = 5 * time.Second

// withConfigLock runs fn while holding an exclusive advisory lock on
// <dir>/.config.lock. If the lock cannot be taken within
// configLockTimeout, or locking is unsupported on this platform, fn
// runs anyway.
//
// The lock is not reentrant. Functions called by fn must use the
// unlocked internal helpers (load, save) rather than the exported
// Load/Save/SetXxx entry points.
func withConfigLock(dir string, fn func() error) error {
	release, err := acquireConfigLock(dir)
	if err == nil {
		defer release()
	}
	return fn()
}
