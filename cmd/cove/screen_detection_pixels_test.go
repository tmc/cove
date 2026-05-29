package main

import (
	"image/color"
	"testing"
)

// Pixel-analysis helpers in screen_detection.go are pure (image, w, h) ->
// bool/int and easy to drive with synthetic images. The tests below cover
// the obvious "definitely yes" / "definitely no" branches.

func TestHasScrollableTextArea(t *testing.T) {
	w, h := 1024, 768

	// Predominantly light center stripe -> looks like a Terms scroll area.
	bright := newSolidImage(w, h, color.RGBA{20, 20, 20, 255})
	fillRect(bright, w/2-w/3, 0, 2*w/3, h, color.RGBA{240, 240, 240, 255})
	if !hasScrollableTextArea(bright, w, h) {
		t.Errorf("hasScrollableTextArea(bright center) = false, want true")
	}

	// All dark -> no scrollable text area.
	dark := newSolidImage(w, h, color.RGBA{20, 20, 20, 255})
	if hasScrollableTextArea(dark, w, h) {
		t.Errorf("hasScrollableTextArea(all dark) = true, want false")
	}
}

func TestHasBlueAccent(t *testing.T) {
	w, h := 800, 600

	// Solid blue center -> Siri-like blue accent.
	blue := newSolidImage(w, h, color.RGBA{0, 0, 0, 255})
	fillRect(blue, 0, 0, w, h, color.RGBA{20, 30, 200, 255})
	if !hasBlueAccent(blue, w, h) {
		t.Errorf("hasBlueAccent(blue) = false, want true")
	}

	// Solid red -> no blue accent.
	red := newSolidImage(w, h, color.RGBA{200, 30, 30, 255})
	if hasBlueAccent(red, w, h) {
		t.Errorf("hasBlueAccent(red) = true, want false")
	}
}

func TestLooksLikeCheckbox(t *testing.T) {
	// White interior with dark border -> classic empty checkbox shape.
	img := newSolidImage(200, 200, color.RGBA{255, 255, 255, 255})
	// Dark border ring at (50,50) size 20.
	x, y, size := 50, 50, 20
	for px := x; px < x+size; px++ {
		img.SetRGBA(px, y, color.RGBA{0, 0, 0, 255})
		img.SetRGBA(px, y+size-1, color.RGBA{0, 0, 0, 255})
	}
	for py := y; py < y+size; py++ {
		img.SetRGBA(x, py, color.RGBA{0, 0, 0, 255})
		img.SetRGBA(x+size-1, py, color.RGBA{0, 0, 0, 255})
	}
	if !looksLikeCheckbox(img, x, y, size) {
		t.Errorf("looksLikeCheckbox(bordered square) = false, want true")
	}

	// Zero size -> no edge/interior pixels, must return false.
	if looksLikeCheckbox(img, 0, 0, 0) {
		t.Errorf("looksLikeCheckbox(size=0) = true, want false")
	}
}

func TestCountCheckboxes(t *testing.T) {
	w, h := 800, 600
	// Blank white image -> avgEdge≈avgInterior≈255, both >100 so each probe
	// satisfies the "filled" branch of looksLikeCheckbox. Just verify that
	// countCheckboxes returns a positive count and exercises the loop.
	white := newSolidImage(w, h, color.RGBA{255, 255, 255, 255})
	if got := countCheckboxes(white, w, h); got <= 0 {
		t.Errorf("countCheckboxes(white) = %d, want > 0", got)
	}

	// Pure black image: avgEdge≈avgInterior≈0, neither branch triggers.
	black := newSolidImage(w, h, color.RGBA{0, 0, 0, 255})
	if got := countCheckboxes(black, w, h); got != 0 {
		t.Errorf("countCheckboxes(black) = %d, want 0", got)
	}
}
