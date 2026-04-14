package main

import "testing"

func TestMapWindowCapturePointToViewPoint(t *testing.T) {
	tests := []struct {
		name                 string
		x, y                 float64
		captureW, captureH   int
		boundsW, contentH    float64
		wantX, wantY         float64
	}{
		{
			name:      "same-sized capture maps directly",
			x:         624.6,
			y:         383.4,
			captureW:  1024,
			captureH:  852,
			boundsW:   1024,
			contentH:  852,
			wantX:     624.6,
			wantY:     468.6,
		},
		{
			name:      "titlebar inset is removed",
			x:         300,
			y:         120,
			captureW:  1024,
			captureH:  852,
			boundsW:   1024,
			contentH:  800,
			wantX:     300,
			wantY:     732,
		},
		{
			name:      "top clicks clamp into content",
			x:         50,
			y:         10,
			captureW:  1024,
			captureH:  852,
			boundsW:   1024,
			contentH:  800,
			wantX:     50,
			wantY:     800,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotX, gotY := mapWindowCapturePointToViewPoint(tt.x, tt.y, tt.captureW, tt.captureH, tt.boundsW, tt.contentH)
			if gotX != tt.wantX || gotY != tt.wantY {
				t.Fatalf("mapWindowCapturePointToViewPoint() = (%.1f, %.1f), want (%.1f, %.1f)", gotX, gotY, tt.wantX, tt.wantY)
			}
		})
	}
}
