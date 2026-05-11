package controlserver

import (
	"testing"

	"github.com/tmc/apple/corefoundation"
)

// TestMouseYMappingUsesContentHeight asserts the slice-6c invariant:
// the mouse Y mapping flips against the cached content height (the VM
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
	// MapNormalizedWindowCapturePointToViewPoint.
	_, viewY := MapNormalizedWindowCapturePointToViewPoint(0.5, normY, 0, 0, boundsW, contentH)

	want := (1.0 - normY) * contentH
	if viewY != want {
		t.Fatalf("viewY = %v, want %v (mapping must use contentH=%v, not bounds height)", viewY, want, contentH)
	}
}

// TestNeedsWindowCapturePointMappingDisabledWhenCaptureZero ensures
// the mapping is skipped when capture dimensions are unknown,
// preserving the legacy (pre-window-mapping) coordinate path.
func TestNeedsWindowCapturePointMappingDisabledWhenCaptureZero(t *testing.T) {
	if NeedsWindowCapturePointMapping(BackendWindow, 0, 0, 1024, 768) {
		t.Fatal("mapping should be disabled when captureW/captureH are 0")
	}
}

// TestInputBridgeZeroValueHasNilHost documents that a zero-value
// InputBridge has no host wired. ControlServer constructors that
// build &ControlServer{} rely on this.
func TestInputBridgeZeroValueHasNilHost(t *testing.T) {
	var b InputBridge
	if b.host != nil {
		t.Fatalf("zero InputBridge.host = %v, want nil", b.host)
	}
}

func TestSetInputBridgeRuntimeInstallsHooks(t *testing.T) {
	_ = t.TempDir()
	tests := []struct {
		name string
		rt   InputBridgeRuntime
	}{
		{
			name: "hooks",
			rt: InputBridgeRuntime{
				CreateMouseEvent: func(uint64, uint32, corefoundation.CGPoint, uint32) (corefoundation.CFTypeRef, error) {
					return corefoundation.CFTypeRef(new(byte)), nil
				},
				CreateKeyboardEvent: func(uint64, uint16, bool) (corefoundation.CFTypeRef, error) {
					return corefoundation.CFTypeRef(new(byte)), nil
				},
				PostEvent:             func(uint32, corefoundation.CFTypeRef) error { return nil },
				SetEventUnicodeString: func(corefoundation.CFTypeRef, string) {},
				SetEventFlags:         func(corefoundation.CFTypeRef, uint64) {},
				RunOnUIThreadSync:     func(f func()) { f() },
				AllowHIDKeyboard:      func() bool { return true },
				ModifierKeySequence:   func(flags uint32) []uint32 { return []uint32{flags + 1} },
				ModifierShift:         1,
				CGEventMouseMoved:     2,
				CGEventLeftMouseDown:  3,
				CGEventRightMouseDown: 4,
				CGEventLeftMouseUp:    5,
				CGEventRightMouseUp:   6,
				CGHIDEventTap:         7,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetInputBridgeRuntime(tt.rt)
			if got, _ := createMouseEventFn(0, 0, corefoundation.CGPoint{}, 0); got == nil {
				t.Fatal("createMouseEventFn returned nil")
			}
			if got, _ := createKeyboardEventFn(0, 0, false); got == nil {
				t.Fatal("createKeyboardEventFn returned nil")
			}
			ran := false
			runOnUIThreadSyncFn(func() { ran = true })
			if !ran || !allowHIDKeyboardFn() {
				t.Fatal("runtime hooks not installed")
			}
			if got := modifierKeySequenceFn(4); len(got) != 1 || got[0] != 5 {
				t.Fatalf("modifierKeySequenceFn = %v, want [5]", got)
			}
			if modifierShiftMask != 1 || cgEventMouseMoved != 2 || cgEventLeftMouseDown != 3 ||
				cgEventRightMouseDown != 4 || cgEventLeftMouseUp != 5 || cgEventRightMouseUp != 6 ||
				cgHIDEventTap != 7 {
				t.Fatal("runtime constants not installed")
			}
		})
	}
}
