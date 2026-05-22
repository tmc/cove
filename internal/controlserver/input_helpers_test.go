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
			name:     "framebuffer backend never maps",
			mode:     BackendFramebuffer,
			captureW: 1024, captureH: 768,
			boundsW: 1024, contentH: 768,
			want: false,
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
		{"invalid capture", 10, 20, 0, 100, 200, 80, 10, 60},
		{"invalid view", 10, 20, 100, 100, 0, 80, 10, 60},
		{"center no inset", 50, 25, 100, 100, 200, 100, 100, 75},
		{"clamps top inset", 50, 10, 100, 120, 200, 100, 100, 100},
		{"clamps bottom", 50, 130, 100, 120, 200, 100, 100, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotX, gotY := MapWindowCapturePointToViewPoint(tt.x, tt.y, tt.captureW, tt.captureH, tt.boundsW, tt.contentH)
			if gotX != tt.wantX || gotY != tt.wantY {
				t.Fatalf("point = (%v,%v), want (%v,%v)", gotX, gotY, tt.wantX, tt.wantY)
			}
		})
	}
}
