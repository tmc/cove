//go:build !darwin

package sckit

import "context"

// StartStream on non-darwin always returns ErrUnsupported.
func StartStream(ctx context.Context, windowID uint32) (*Stream, error) {
	return nil, ErrUnsupported
}
