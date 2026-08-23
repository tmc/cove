package main

import (
	"image"
	"image/color"
	"testing"
)

func TestShouldFadeBootOverlay(t *testing.T) {
	base := bootOverlayFadeInput{maxHoldFrames: 100}
	tests := []struct {
		name string
		in   bootOverlayFadeInput
		want bool
	}{
		{"not running", base, false},
		{
			"running, probe says still black",
			bootOverlayFadeInput{vmRunning: true, paintProbeWorks: true, maxHoldFrames: 100},
			false,
		},
		{
			"running, guest painted",
			bootOverlayFadeInput{vmRunning: true, paintProbeWorks: true, guestPainted: true, maxHoldFrames: 100},
			true,
		},
		{
			"running, no usable probe",
			bootOverlayFadeInput{vmRunning: true, maxHoldFrames: 100},
			true,
		},
		{
			"first-boot hold outranks a painted frame",
			bootOverlayFadeInput{vmRunning: true, holdForFirstBoot: true, paintProbeWorks: true, guestPainted: true, maxHoldFrames: 100},
			false,
		},
		{
			"agent ready releases the first-boot hold",
			bootOverlayFadeInput{vmRunning: true, holdForFirstBoot: true, agentReady: true, paintProbeWorks: true, maxHoldFrames: 100},
			true,
		},
		{
			"timeout releases a never-painting guest",
			bootOverlayFadeInput{vmRunning: true, paintProbeWorks: true, heldFrames: 100, maxHoldFrames: 100},
			true,
		},
		{
			"timeout releases a stalled first boot",
			bootOverlayFadeInput{holdForFirstBoot: true, heldFrames: 200, maxHoldFrames: 100},
			true,
		},
		{
			"no timeout configured never force-fades",
			bootOverlayFadeInput{heldFrames: 1000},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldFadeBootOverlay(tt.in); got != tt.want {
				t.Fatalf("shouldFadeBootOverlay(%+v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func fillImage(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func TestGuestFrameHasContent(t *testing.T) {
	black := fillImage(200, 200, color.RGBA{A: 255})
	if guestFrameHasContent(black) {
		t.Fatal("guestFrameHasContent(black) = true, want false")
	}
	if guestFrameHasContent(nil) {
		t.Fatal("guestFrameHasContent(nil) = true, want false")
	}

	bright := fillImage(200, 200, color.RGBA{R: 200, G: 200, B: 200, A: 255})
	if !guestFrameHasContent(bright) {
		t.Fatal("guestFrameHasContent(bright) = false, want true")
	}

	// A boot logo: a small white glyph on black. Mean brightness stays under
	// the black threshold, but the frame is clearly not blank.
	logo := fillImage(200, 200, color.RGBA{A: 255})
	for y := 90; y < 115; y++ {
		for x := 90; x < 115; x++ {
			logo.SetRGBA(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	if DetectScreenState(logo) != ScreenStateBlack {
		t.Fatalf("test setup: logo frame classifies as %v, want black", DetectScreenState(logo))
	}
	if !guestFrameHasContent(logo) {
		t.Fatal("guestFrameHasContent(boot logo) = false, want true")
	}
}
