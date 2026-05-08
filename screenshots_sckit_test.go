package main

import (
	"context"
	"errors"
	"image"
	"testing"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/objc"
)

// stubVMViewWindow installs the minimal window/windowNum state required
// by captureVMView. The window handle is a synthetic nonzero objc.ID;
// the CGWindow fallback path will fail to capture (returning a nil
// image error) but that is sufficient to exercise the dispatch.
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

func resetSCKitFallbackOnce(t *testing.T) {
	t.Helper()
	clear := func() {
		sckitFallbackOnce.Range(func(k, _ any) bool { sckitFallbackOnce.Delete(k); return true })
	}
	clear()
	t.Cleanup(clear)
}

func TestCaptureVMViewDispatchesToSCKitWhenOptedIn(t *testing.T) {
	t.Setenv("COVE_CAPTURE_BACKEND", "sckit")
	resetSCKitFallbackOnce(t)

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

func TestCaptureVMViewSCKitErrorFallsThroughToCGWindow(t *testing.T) {
	t.Setenv("COVE_CAPTURE_BACKEND", "sckit")
	resetSCKitFallbackOnce(t)

	withCaptureSCKitFn(t, func(ctx context.Context, windowID uint32) (image.Image, error) {
		return nil, errors.New("TCC denied")
	})

	s := NewControlServerWithVMDir("", "")
	stubVMViewWindow(s, 99999) // bogus ID; CGWindowList returns nil

	_, errMsg := s.captureVMView()
	// Fallback ran (we reached CGWindowList path), and CGWindowList
	// surfaced its own error string. The capture call did not fail
	// hard — the operator sees the existing CGWindow degradation, not
	// a TCC error.
	if errMsg == "" {
		t.Fatal("expected fallback CGWindow error string, got empty")
	}
}

func TestCaptureVMViewCGWindowDefaultBypassesSCKit(t *testing.T) {
	t.Setenv("COVE_CAPTURE_BACKEND", "")
	resetSCKitFallbackOnce(t)

	called := 0
	withCaptureSCKitFn(t, func(ctx context.Context, windowID uint32) (image.Image, error) {
		called++
		return nil, errors.New("should not be called")
	})

	s := NewControlServerWithVMDir("", "")
	stubVMViewWindow(s, 7)

	_, _ = s.captureVMView()
	if called != 0 {
		t.Fatalf("captureSCKitFn called %d times in cgwindow mode, want 0", called)
	}
}

func TestWarnSCKitFallbackOncePerCause(t *testing.T) {
	resetSCKitFallbackOnce(t)
	// We can't observe slog output without a handler, but we can
	// verify the sync.Map seeds one *sync.Once per cause.
	warnSCKitFallbackOnce("tcc", errors.New("denied"))
	warnSCKitFallbackOnce("tcc", errors.New("denied again"))
	warnSCKitFallbackOnce("timeout", errors.New("ctx"))

	count := 0
	sckitFallbackOnce.Range(func(_, _ any) bool {
		count++
		return true
	})
	if count != 2 {
		t.Fatalf("distinct causes recorded = %d, want 2", count)
	}
}

func TestClassifySCKitError(t *testing.T) {
	tests := []struct {
		msg, want string
	}{
		{"TCC denied", "tcc"},
		{"shareable content: not authorized", "tcc"},
		{"window 42 not in shareable content (TCC denied or off-screen)", "tcc"},
		{"window 42 off-screen", "window-missing"},
		{"context deadline exceeded", "timeout"},
		{"random kernel panic", "other"},
	}
	for _, tt := range tests {
		if got := classifySCKitError(tt.msg); got != tt.want {
			t.Errorf("classifySCKitError(%q) = %q, want %q", tt.msg, got, tt.want)
		}
	}
}
