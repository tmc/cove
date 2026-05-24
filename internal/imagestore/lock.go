package imagestore

import (
	"errors"
	"fmt"
)

var errLockUnsupported = errors.New("image.lock: unsupported platform")

// AcquireLock takes a blocking exclusive flock on <imageDir>/.image.lock.
// The image directory must already exist.
func AcquireLock(imageDir string) (*Lock, error) {
	return acquireLock(imageDir, false)
}

// TryAcquireLock is the non-blocking variant.
func TryAcquireLock(imageDir string) (*Lock, error) {
	return acquireLock(imageDir, true)
}

func checkLockDir(imageDir string) error {
	if imageDir == "" {
		return errors.New("image.lock: imageDir required")
	}
	return nil
}

// Lock guards an image directory until Release is called.
type Lock struct {
	release func() error
}

// Release drops the lock. It is safe to call more than once.
func (l *Lock) Release() error {
	if l == nil || l.release == nil {
		return nil
	}
	err := l.release()
	l.release = nil
	return err
}

func unsupportedLock(imageDir string) (*Lock, error) {
	if err := checkLockDir(imageDir); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("%w", errLockUnsupported)
}
