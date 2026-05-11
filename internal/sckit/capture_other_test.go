//go:build !darwin

package sckit

import (
	"context"
	"errors"
	"testing"
)

func TestCaptureWindowUnsupported(t *testing.T) {
	tests := []struct {
		name     string
		windowID uint32
	}{
		{name: "zero", windowID: 0},
		{name: "nonzero", windowID: 42},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			img, err := CaptureWindow(context.Background(), tt.windowID)
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("CaptureWindow(%d) error = %v, want %v", tt.windowID, err, ErrUnsupported)
			}
			if img != nil {
				t.Fatalf("CaptureWindow(%d) image = %v, want nil", tt.windowID, img)
			}
		})
	}
}
