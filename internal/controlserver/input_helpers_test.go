package controlserver

import "testing"

func TestNeedsWindowCapturePointMapping(t *testing.T) {
	tests := []struct {
		name                 string
		mode                 BackendMode
		captureW, captureH   int
		boundsW, contentH    float64
		want                 bool
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
