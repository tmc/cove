package main

import "testing"

// TestMouseYMappingUsesContentHeight asserts the slice-6c invariant: the
// mouse Y mapping flips against the cached content height (the VM
// content area, e.g. 768px), not the NSView bounds height (which
// includes the 32px title bar). The catch case is a non-window backend
// with no capture metadata: viewY must equal (1.0 - normY) * contentH.
func TestMouseYMappingUsesContentHeight(t *testing.T) {
	const (
		boundsW  = 1024.0
		contentH = 768.0
		normY    = 0.25
	)

	// captureW=0 / captureH=0 forces the no-capture branch in
	// mapNormalizedWindowCapturePointToViewPoint.
	_, viewY := mapNormalizedWindowCapturePointToViewPoint(0.5, normY, 0, 0, boundsW, contentH)

	want := (1.0 - normY) * contentH
	if viewY != want {
		t.Fatalf("viewY = %v, want %v (mapping must use contentH=%v, not bounds height)", viewY, want, contentH)
	}
}

// TestNeedsWindowCapturePointMappingDisabledWhenCaptureZero ensures
// the mapping is skipped when capture dimensions are unknown,
// preserving the legacy (pre-window-mapping) coordinate path.
func TestNeedsWindowCapturePointMappingDisabledWhenCaptureZero(t *testing.T) {
	if needsWindowCapturePointMapping(automationBackendWindow, 0, 0, 1024, 768) {
		t.Fatal("mapping should be disabled when captureW/captureH are 0")
	}
}

// TestInputBridgeZeroValueHasNilCS documents that a zero-value
// inputBridge has no parent ControlServer wired. Tests that build
// &ControlServer{} rely on this.
func TestInputBridgeZeroValueHasNilCS(t *testing.T) {
	var b inputBridge
	if b.cs != nil {
		t.Fatalf("zero inputBridge.cs = %v, want nil", b.cs)
	}
}
