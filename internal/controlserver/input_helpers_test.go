package controlserver

import (
	"testing"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestKeyboardEventUnicodeString(t *testing.T) {
	_ = t.TempDir()
	tests := []struct {
		name string
		cmd  *controlpb.KeyCommand
		want string
	}{
		{"nil", nil, ""},
		{"character wins", &controlpb.KeyCommand{Character: "x", KeyCode: 36}, "x"},
		{"return", &controlpb.KeyCommand{KeyCode: 36}, "\r"},
		{"tab", &controlpb.KeyCommand{KeyCode: 48}, "\t"},
		{"delete", &controlpb.KeyCommand{KeyCode: 51}, "\x7f"},
		{"escape", &controlpb.KeyCommand{KeyCode: 53}, "\x1b"},
		{"space", &controlpb.KeyCommand{KeyCode: 49}, " "},
		{"unknown", &controlpb.KeyCommand{KeyCode: 123}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := KeyboardEventUnicodeString(tt.cmd); got != tt.want {
				t.Fatalf("KeyboardEventUnicodeString = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNeedsWindowCapturePointMapping(t *testing.T) {
	tests := []struct {
		name               string
		mode               BackendMode
		captureW, captureH int
		boundsW, contentH  float64
		want               bool
	}{
		{
			name:     "capture width zero disables mapping",
			mode:     BackendWindow,
			captureW: 0, captureH: 768,
			boundsW: 1024, contentH: 768,
			want: false,
		},
		{
			name:     "capture height zero disables mapping",
			mode:     BackendWindow,
			captureW: 1024, captureH: 0,
			boundsW: 1024, contentH: 768,
			want: false,
		},
		{
			name:     "window backend always maps when capture set",
			mode:     BackendWindow,
			captureW: 800, captureH: 600,
			boundsW: 1024, contentH: 768,
			want: true,
		},
		{
			name:     "framebuffer with matching dimensions skips mapping",
			mode:     BackendFramebuffer,
			captureW: 1024, captureH: 768,
			boundsW: 1024, contentH: 768,
			want: false,
		},
		{
			name:     "framebuffer cache includes top inset",
			mode:     BackendFramebuffer,
			captureW: 1024, captureH: 852,
			boundsW: 1024, contentH: 800,
			want: true,
		},
		{
			name:     "auto with matching dims skips mapping",
			mode:     BackendAuto,
			captureW: 1024, captureH: 768,
			boundsW: 1024, contentH: 768,
			want: false,
		},
		{
			name:     "auto with mismatched width maps",
			mode:     BackendAuto,
			captureW: 800, captureH: 768,
			boundsW: 1024, contentH: 768,
			want: true,
		},
		{
			name:     "auto with mismatched height maps",
			mode:     BackendAuto,
			captureW: 1024, captureH: 600,
			boundsW: 1024, contentH: 768,
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NeedsWindowCapturePointMapping(tt.mode, tt.captureW, tt.captureH, tt.boundsW, tt.contentH)
			if got != tt.want {
				t.Fatalf("NeedsWindowCapturePointMapping(%v, %d, %d, %v, %v) = %v, want %v",
					tt.mode, tt.captureW, tt.captureH, tt.boundsW, tt.contentH, got, tt.want)
			}
		})
	}
}

func TestMapWindowCapturePointToViewPoint(t *testing.T) {
	_ = t.TempDir()
	tests := []struct {
		name               string
		x, y               float64
		captureW, captureH int
		boundsW, contentH  float64
		wantX, wantY       float64
	}{
		// Degenerate inputs fall back to a plain top-origin flip.
		{"invalid capture", 10, 20, 0, 100, 200, 80, 10, 60},
		{"setup full name below cached top inset", 512, 409, 1024, 852, 1024, 800, 512, 443},
		{"invalid view", 10, 20, 100, 100, 0, 80, 10, 60},

		// 1x capture, no title bar: capture and view coincide, so the
		// point is only Y-flipped.
		{"1x no title bar center", 50, 30, 100, 100, 100, 100, 50, 70},
		{"1x no title bar top", 50, 0, 100, 100, 100, 100, 50, 100},
		{"1x no title bar bottom", 50, 100, 100, 100, 100, 100, 50, 0},

		// 1x capture with a 20pt title bar band (captureH 120 = 20
		// title + 100 content). A capture-top click strips into the
		// title band and clamps to the content top.
		{"1x title bar strips band", 50, 20, 100, 120, 100, 100, 50, 100},
		{"1x title bar mid content", 50, 70, 100, 120, 100, 100, 50, 50},
		{"1x title bar clamps top", 50, 10, 100, 120, 100, 100, 50, 100},
		{"1x title bar clamps bottom", 50, 130, 100, 120, 100, 100, 50, 0},

		// 2x Retina host, no title bar: width ratio recovers the scale
		// and the point converts into logical points.
		{"2x no title bar center", 100, 100, 200, 200, 100, 100, 50, 50},
		{"2x no title bar bottom", 100, 200, 200, 200, 100, 100, 50, 0},

		// 2x Retina host with a 28pt title bar — the live cove-test
		// geometry (capture 1634x938 device px, view 817x441 points).
		// A bottom-of-content consent button must land near viewY 0,
		// NOT get clamped to the menu bar at viewY contentH.
		{"live retina allow button", 0.5 * 1634, 0.95 * 938, 1634, 938, 817, 441, 408.5, 23.45},
		{"live retina top sidebar", 0.141 * 1634, 0.223 * 938, 1634, 938, 817, 441, 115.197, 364.413},
		{"live retina center", 0.5 * 1634, 0.5 * 938, 1634, 938, 817, 441, 408.5, 234.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotX, gotY := MapWindowCapturePointToViewPoint(tt.x, tt.y, tt.captureW, tt.captureH, tt.boundsW, tt.contentH)
			if !floatNear(gotX, tt.wantX) || !floatNear(gotY, tt.wantY) {
				t.Fatalf("point = (%v,%v), want (%v,%v)", gotX, gotY, tt.wantX, tt.wantY)
			}
		})
	}
}

func floatNear(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	// These are pixel/point coordinates; sub-milli-pixel precision is
	// meaningless and float round-trips vary in the last ulps.
	return d < 1e-3
}
