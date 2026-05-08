//go:build darwin && sckit_live

package sckit

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestCaptureWindowLive exercises a real SCKit capture against a window
// owned by the current process. It is gated on COVE_TEST_SCKIT_GRANT=1
// so CI never triggers a Screen Recording TCC prompt; release engineers
// run it once per release on a TCC-granted host.
//
// Build: go test -tags sckit_live ./internal/sckit/
func TestCaptureWindowLive(t *testing.T) {
	if os.Getenv("COVE_TEST_SCKIT_GRANT") != "1" {
		t.Skip("set COVE_TEST_SCKIT_GRANT=1 on a TCC-granted host to run")
	}
	wid := os.Getenv("COVE_TEST_SCKIT_WINDOW_ID")
	if wid == "" {
		t.Skip("set COVE_TEST_SCKIT_WINDOW_ID=<CGWindowID> to identify the target window")
	}
	var windowID uint32
	for _, c := range wid {
		if c < '0' || c > '9' {
			t.Fatalf("invalid COVE_TEST_SCKIT_WINDOW_ID %q", wid)
		}
		windowID = windowID*10 + uint32(c-'0')
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	img, err := CaptureWindow(ctx, windowID)
	if err != nil {
		t.Fatalf("CaptureWindow: %v", err)
	}
	if img == nil {
		t.Fatal("CaptureWindow returned nil image")
	}
	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		t.Fatalf("image bounds = %v, want non-empty", b)
	}
}
