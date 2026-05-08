// image_lock.go — exclusive advisory lock on a single image directory.
//
// Mirrors run_lock.go for the image store. Held by MaterializeImage
// (clonefile + child config write), TagImage (clone + manifest
// rename), and gc (recheck + RemoveAll). Closes R1, R2, R3, R7 in
// docs/research/image-gc-race-audit-2026-05-08.md. The kernel drops
// the flock on descriptor close, so crashed holders never leak.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

const imageLockFile = ".image.lock"

// ImageLock holds the open descriptor whose flock guards an image dir.
type ImageLock struct{ f *os.File }

// AcquireImageLock takes a blocking exclusive flock on
// <imageDir>/.image.lock. The image directory must already exist.
func AcquireImageLock(imageDir string) (*ImageLock, error) {
	return acquireImageLock(imageDir, false)
}

// TryAcquireImageLock is the non-blocking variant.
func TryAcquireImageLock(imageDir string) (*ImageLock, error) {
	return acquireImageLock(imageDir, true)
}

func acquireImageLock(imageDir string, nonblock bool) (*ImageLock, error) {
	if imageDir == "" {
		return nil, errors.New("image.lock: imageDir required")
	}
	path := filepath.Join(imageDir, imageLockFile)
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
	return &ImageLock{f: f}, nil
}

// Release drops the flock by closing the descriptor. Safe to re-call.
func (l *ImageLock) Release() error {
	if l == nil || l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}

// acquireImageLockHook lets tests stub out lock acquisition; mirrors
// acquireRunLockHook in runtime_lifecycle.go.
var acquireImageLockHook = AcquireImageLock
