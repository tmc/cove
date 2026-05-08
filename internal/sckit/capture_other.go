//go:build !darwin

package sckit

import (
	"context"
	"errors"
	"image"
)

// CaptureWindow on non-darwin always returns ErrUnsupported.
func CaptureWindow(ctx context.Context, windowID uint32) (image.Image, error) {
	return nil, ErrUnsupported
}

// ErrUnsupported is returned by CaptureWindow on non-darwin builds.
var ErrUnsupported = errors.New("sckit: ScreenCaptureKit not supported on this platform")
