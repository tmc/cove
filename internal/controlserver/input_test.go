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

// TestMapNormalizedRetinaTitleBarHitsContent is the regression for the
// NetworkExtension consent misroute: on a Retina host the window-capture
// image is in device pixels (e.g. 1634x938) while the VM view Bounds are
// logical points (817x441) and cover only the content area below a 28pt
// title bar. The old mapping subtracted a device-pixel captureH from a
// point-valued contentH, clamping every click to the top of the view so
// a bottom-of-content Allow button landed on the guest menu bar.
//
// This asserts the corrected mapping: normalized inputs across the
// content flip into view points with the correct backing scale, a
// bottom-of-content target lands in the lower content band (small viewY),
// and a top-of-content target lands in the upper band (large viewY) —
// never the reverse.
func TestMapNormalizedRetinaTitleBarHitsContent(t *testing.T) {
	const (
		captureW = 1634 // device px (2x of 817pt window width)
		captureH = 938  // device px (2x of 469pt window height)
		boundsW  = 817.0
		contentH = 441.0 // 469pt window - 28pt title bar
	)

	tests := []struct {
		name       string
		normX      float64
		normY      float64
		wantX      float64
		wantY      float64
		wantBandLo float64 // inclusive viewY lower bound
		wantBandHi float64 // inclusive viewY upper bound
	}{
		{
			name: "allow button bottom-center", normX: 0.5, normY: 0.95,
			wantX: 408.5, wantY: 23.45, wantBandLo: 0, wantBandHi: 0.15 * contentH,
		},
		{
			name: "top sidebar rules", normX: 0.141, normY: 0.223,
			wantX: 115.197, wantY: 364.413,
			wantBandLo: 0.7 * contentH, wantBandHi: contentH,
		},
		{
			name: "dead center", normX: 0.5, normY: 0.5,
			wantX: 408.5, wantY: 234.5,
			wantBandLo: 0.4 * contentH, wantBandHi: 0.6 * contentH,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viewX, viewY := MapNormalizedWindowCapturePointToViewPoint(
				tt.normX, tt.normY, captureW, captureH, boundsW, contentH)
			if !floatNear(viewX, tt.wantX) || !floatNear(viewY, tt.wantY) {
				t.Fatalf("view = (%v,%v), want (%v,%v)", viewX, viewY, tt.wantX, tt.wantY)
			}
			// The load-bearing anti-menu-bar assertion: the Y must land
			// in the expected content band. A bottom-of-content click
			// (small viewY) must never bubble up to the top (large viewY).
			if viewY < tt.wantBandLo || viewY > tt.wantBandHi {
				t.Fatalf("viewY = %v out of expected band [%v,%v] — coordinate misroute",
					viewY, tt.wantBandLo, tt.wantBandHi)
			}
			// viewY must stay inside the content area, never above the
			// title bar (which would be viewY > contentH).
			if viewY < 0 || viewY > contentH {
				t.Fatalf("viewY = %v escaped content area [0,%v]", viewY, contentH)
			}
		})
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

// TestKeyInjectorOrderBackgroundSafe asserts the invariant-3 boundary:
// the cgevent injector (which activates the VM window and posts through
// the host HID event tap) is reachable only from the explicit window
// input backend or a UseCgEvent request. Auto and framebuffer backends
// must stay background-safe so control-socket automation never steals
// host focus.
func TestKeyInjectorOrderBackgroundSafe(t *testing.T) {
	tests := []struct {
		name       string
		backend    BackendMode
		useCgEvent bool
		allowHID   bool
		want       []string
	}{
		{"auto", BackendAuto, false, false, []string{"nsevent"}},
		{"auto with hid", BackendAuto, false, true, []string{"nsevent", "private"}},
		{"framebuffer", BackendFramebuffer, false, true, []string{"nsevent", "private"}},
		{"window backend", BackendWindow, false, false, []string{"nsevent", "cgevent"}},
		{"explicit cgevent", BackendAuto, true, false, []string{"cgevent", "nsevent"}},
		{"explicit cgevent with hid", BackendAuto, true, true, []string{"private", "cgevent", "nsevent"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := keyInjectorOrder(tt.backend, tt.useCgEvent, tt.allowHID)
			if len(got) != len(tt.want) {
				t.Fatalf("keyInjectorOrder = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("keyInjectorOrder = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

// TestPointerDeliveryPathDefaultsToView locks the delivery-path choice:
// the direct-backend default (and every backend with no explicit
// override) must select the AppKit "view" path, never the private "hid"
// selector. The HID path silently swallows events on current hardware and
// is reachable only as an explicit COVE_POINTER_DELIVERY=hid diagnostic.
// The AppKit path stays background-safe (no CGEvent, no window
// activation), preserving invariant 3.
func TestPointerDeliveryPathDefaultsToView(t *testing.T) {
	tests := []struct {
		name     string
		backend  BackendMode
		override string
		want     string
	}{
		{"framebuffer default", BackendFramebuffer, "", "view"},
		{"auto default", BackendAuto, "", "view"},
		{"window default", BackendWindow, "", "view"},
		{"framebuffer explicit hid", BackendFramebuffer, "hid", "hid"},
		{"auto explicit hid", BackendAuto, "hid", "hid"},
		{"framebuffer explicit view", BackendFramebuffer, "view", "view"},
		{"unrecognized override falls back to view", BackendAuto, "garbage", "view"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pointerDeliveryPath(tt.backend, tt.override); got != tt.want {
				t.Fatalf("pointerDeliveryPath(%v, %q) = %q, want %q",
					tt.backend, tt.override, got, tt.want)
			}
		})
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
					var ref corefoundation.CFTypeRef
					return ref, nil
				},
				CreateKeyboardEvent: func(uint64, uint16, bool) (corefoundation.CFTypeRef, error) {
					var ref corefoundation.CFTypeRef
					return ref, nil
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
			if _, err := createMouseEventFn(0, 0, corefoundation.CGPoint{}, 0); err != nil {
				t.Fatalf("createMouseEventFn: %v", err)
			}
			if _, err := createKeyboardEventFn(0, 0, false); err != nil {
				t.Fatalf("createKeyboardEventFn: %v", err)
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
