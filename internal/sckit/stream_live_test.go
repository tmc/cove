//go:build darwin && sckit_live

package sckit

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestStartStreamLive exercises a real persistent SCStream against a
// window owned by the current session. Gated exactly like
// TestCaptureWindowLive so CI never triggers a Screen Recording TCC
// prompt.
//
// Build: go test -tags sckit_live ./internal/sckit/
func TestStartStreamLive(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	s, err := StartStream(ctx, windowID)
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	defer s.Stop(ctx)

	// Frames only arrive on content change; poll until one lands or
	// time out. A static window may legitimately never repaint, so a
	// timeout here is reported but not fatal unless zero frames AND a
	// stream stop error occurred.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if img, at, err := s.Snapshot(); err == nil {
			b := img.Bounds()
			if b.Dx() <= 0 || b.Dy() <= 0 {
				t.Fatalf("frame bounds = %v, want non-empty", b)
			}
			t.Logf("frame %dx%d at %v after %d frames", b.Dx(), b.Dy(), at, s.Frames())
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	if stopped, serr := s.Stopped(); stopped {
		t.Fatalf("stream stopped without frames: %v", serr)
	}
	t.Log("no frame within 10s (window may be static); stream still running")
}
