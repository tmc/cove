//go:build !darwin && !linux && !freebsd && !netbsd && !openbsd && !dragonfly

package imagestore

func acquireLock(imageDir string, nonblock bool) (*Lock, error) {
	return unsupportedLock(imageDir)
}
