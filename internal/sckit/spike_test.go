//go:build darwin

package sckit

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCaptureSpikeWindowErrors(t *testing.T) {
	p := Detect()
	if !p.SCKitAvailable {
		t.Skip("ScreenCaptureKit unavailable")
	}
	if !p.ScreenRecordingAuthorized {
		t.Skip("Screen Recording not authorized")
	}

	tests := []struct {
		name     string
		windowID uint32
		want     error
	}{
		{name: "missing window", windowID: ^uint32(0), want: ErrWindowNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			img, _, err := CaptureSpike(ctx, tt.windowID)
			if !errors.Is(err, tt.want) {
				t.Fatalf("CaptureSpike(%d) error = %v, want %v", tt.windowID, err, tt.want)
			}
			if img != nil {
				t.Fatalf("CaptureSpike(%d) image = %v, want nil", tt.windowID, img)
			}
		})
	}
}
