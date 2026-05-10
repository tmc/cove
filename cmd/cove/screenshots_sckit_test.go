package main

import (
	"context"
	"errors"
	"image"
	"testing"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/objc"
)

func stubVMViewWindow(s *ControlServer, windowNum int) {
	s.mu.Lock()
	s.window = appkit.NSWindowFromID(objc.ID(0xdead))
	s.windowNum = windowNum
	s.mu.Unlock()
}

func withCaptureSCKitFn(t *testing.T, fn func(context.Context, uint32) (image.Image, error)) {
	t.Helper()
	prev := captureSCKitFn
	captureSCKitFn = fn
	t.Cleanup(func() { captureSCKitFn = prev })
}

func TestCaptureVMViewUsesSCKit(t *testing.T) {
	called := 0
	want := image.NewRGBA(image.Rect(0, 0, 4, 4))
	withCaptureSCKitFn(t, func(ctx context.Context, windowID uint32) (image.Image, error) {
		called++
		if windowID != 7 {
			t.Errorf("windowID = %d, want 7", windowID)
		}
		return want, nil
	})

	s := NewControlServerWithVMDir("", "")
	stubVMViewWindow(s, 7)

	got, errMsg := s.captureVMView()
	if errMsg != "" {
		t.Fatalf("captureVMView errMsg = %q, want empty", errMsg)
	}
	if got != want {
		t.Fatalf("captureVMView image = %v, want %v", got, want)
	}
	if called != 1 {
		t.Fatalf("captureSCKitFn called %d times, want 1", called)
	}
}

func TestCaptureVMViewReturnsSCKitError(t *testing.T) {
	withCaptureSCKitFn(t, func(ctx context.Context, windowID uint32) (image.Image, error) {
		return nil, errors.New("TCC denied")
	})

	s := NewControlServerWithVMDir("", "")
	stubVMViewWindow(s, 99999)

	_, errMsg := s.captureVMView()
	if errMsg != "TCC denied" {
		t.Fatalf("captureVMView errMsg = %q, want TCC denied", errMsg)
	}
}

func TestCaptureVMViewCGWindowEnvStillUsesSCKit(t *testing.T) {
	t.Setenv("COVE_CAPTURE_BACKEND", "cgwindow")

	withCaptureSCKitFn(t, func(ctx context.Context, windowID uint32) (image.Image, error) {
		return image.NewRGBA(image.Rect(0, 0, 4, 4)), nil
	})

	s := NewControlServerWithVMDir("", "")
	stubVMViewWindow(s, 7)

	if _, errMsg := s.captureVMView(); errMsg != "" {
		t.Fatalf("captureVMView errMsg = %q, want empty", errMsg)
	}
}
