package main

import (
	"context"
	"errors"
	"image"
	"testing"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/objc"
	"github.com/tmc/vz-macos/internal/controlserver"
)

type recordingCaptureMetrics struct {
	events []controlserver.CaptureLatencyEvent
}

func (r *recordingCaptureMetrics) EmitCaptureLatency(ctx context.Context, e controlserver.CaptureLatencyEvent) {
	r.events = append(r.events, e)
}

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

func TestCaptureDisplayImageEmitsSCKitLatency(t *testing.T) {
	t.Setenv("COVE_CAPTURE_BACKEND", "sckit")
	resetSCKitFallbackOnce(t)

	want := image.NewRGBA(image.Rect(0, 0, 4, 5))
	withCaptureSCKitFn(t, func(ctx context.Context, windowID uint32) (image.Image, error) {
		return want, nil
	})

	s := NewControlServerWithVMDir("", "")
	rec := &recordingCaptureMetrics{}
	s.capture.SetMetrics(rec)
	stubVMViewWindow(s, 7)

	got, errMsg := s.captureDisplayImage()
	if errMsg != "" {
		t.Fatalf("captureDisplayImage errMsg = %q, want empty", errMsg)
	}
	if got != want {
		t.Fatalf("captureDisplayImage image = %v, want %v", got, want)
	}
	if len(rec.events) != 1 {
		t.Fatalf("events = %d, want 1", len(rec.events))
	}
	event := rec.events[0]
	if event.Backend != "sckit" || event.RequestedBackend != "sckit" {
		t.Fatalf("backend labels = %q/%q, want sckit/sckit", event.Backend, event.RequestedBackend)
	}
	if event.Fallback {
		t.Fatal("Fallback = true, want false")
	}
	if event.Status != "ok" || event.Width != 4 || event.Height != 5 {
		t.Fatalf("event status/size = %q %dx%d, want ok 4x5", event.Status, event.Width, event.Height)
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

func TestCaptureDisplayImageEmitsSCKitFallbackLatency(t *testing.T) {
	t.Setenv("COVE_CAPTURE_BACKEND", "sckit")
	resetSCKitFallbackOnce(t)

	withCaptureSCKitFn(t, func(ctx context.Context, windowID uint32) (image.Image, error) {
		return nil, errors.New("TCC denied")
	})

	s := NewControlServerWithVMDir("", "")
	rec := &recordingCaptureMetrics{}
	s.capture.SetMetrics(rec)
	stubVMViewWindow(s, 99999)

	_, errMsg := s.captureDisplayImage()
	if errMsg == "" {
		t.Fatal("expected fallback CGWindow error string, got empty")
	}
	if len(rec.events) != 1 {
		t.Fatalf("events = %d, want 1", len(rec.events))
	}
	event := rec.events[0]
	if event.Backend != "cgwindow" || event.RequestedBackend != "sckit" {
		t.Fatalf("backend labels = %q/%q, want cgwindow/sckit", event.Backend, event.RequestedBackend)
	}
	if !event.Fallback || event.FallbackCause != "tcc" {
		t.Fatalf("fallback = %v cause %q, want true/tcc", event.Fallback, event.FallbackCause)
	}
	if event.Status != "error" || event.Error == "" {
		t.Fatalf("event status/error = %q/%q, want error/non-empty", event.Status, event.Error)
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

func TestCaptureDisplayImageEmitsCGWindowLatency(t *testing.T) {
	t.Setenv("COVE_CAPTURE_BACKEND", "")
	resetSCKitFallbackOnce(t)

	called := 0
	withCaptureSCKitFn(t, func(ctx context.Context, windowID uint32) (image.Image, error) {
		called++
		return nil, errors.New("should not be called")
	})

	s := NewControlServerWithVMDir("", "")
	rec := &recordingCaptureMetrics{}
	s.capture.SetMetrics(rec)
	stubVMViewWindow(s, 7)

	_, _ = s.captureDisplayImage()
	if called != 0 {
		t.Fatalf("captureSCKitFn called %d times in cgwindow mode, want 0", called)
	}
	if len(rec.events) != 1 {
		t.Fatalf("events = %d, want 1", len(rec.events))
	}
	event := rec.events[0]
	if event.Backend != "cgwindow" || event.RequestedBackend != "cgwindow" {
		t.Fatalf("backend labels = %q/%q, want cgwindow/cgwindow", event.Backend, event.RequestedBackend)
	}
	if event.Fallback {
		t.Fatal("Fallback = true, want false")
	}
}

func TestCaptureDisplayImageEmitsFramebufferLatency(t *testing.T) {
	s := NewControlServerWithVMDir("", "")
	s.setCaptureBackend(automationBackendFramebuffer)
	rec := &recordingCaptureMetrics{}
	s.capture.SetMetrics(rec)

	_, errMsg := s.captureDisplayImage()
	if errMsg == "" {
		t.Fatal("expected framebuffer error string, got empty")
	}
	if len(rec.events) != 1 {
		t.Fatalf("events = %d, want 1", len(rec.events))
	}
	event := rec.events[0]
	if event.Backend != "framebuffer" || event.RequestedBackend != "framebuffer" {
		t.Fatalf("backend labels = %q/%q, want framebuffer/framebuffer", event.Backend, event.RequestedBackend)
	}
	if event.Status != "error" || event.Error == "" {
		t.Fatalf("event status/error = %q/%q, want error/non-empty", event.Status, event.Error)
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

func TestSckitFallbackCauseClassifies(t *testing.T) {
	if got := sckitFallbackCause(nil); got != "nil-image" {
		t.Errorf("sckitFallbackCause(nil) = %q, want nil-image", got)
	}
	if got := sckitFallbackCause(errors.New("TCC denied")); got != "tcc" {
		t.Errorf("sckitFallbackCause(TCC) = %q, want tcc", got)
	}
	if got := sckitFallbackCause(errors.New("kernel panic")); got != "other" {
		t.Errorf("sckitFallbackCause(other) = %q, want other", got)
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
