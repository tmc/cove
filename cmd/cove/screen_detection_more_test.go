package main

import (
	"image"
	"image/color"
	"testing"
)

// fillRect paints a solid color into a sub-rectangle of img.
func fillRect(img *image.RGBA, x, y, w, h int, c color.RGBA) {
	for py := y; py < y+h; py++ {
		for px := x; px < x+w; px++ {
			img.SetRGBA(px, py, c)
		}
	}
}

func TestDetectScreenState_AppleLogo(t *testing.T) {
	// Mostly-dark image with a brighter centered region, mimicking the
	// Apple boot logo. Triggers the centerBrightness > cornerBrightness*1.5
	// branch with overallBrightness < 30.
	w, h := 1024, 768
	img := newSolidImage(w, h, color.RGBA{12, 12, 12, 255})
	fillRect(img, w/2-150, h/2-150, 300, 300, color.RGBA{200, 200, 200, 255})

	if got := DetectScreenState(img); got != ScreenStateAppleLogo {
		t.Errorf("DetectScreenState(apple-logo image) = %v, want %v", got, ScreenStateAppleLogo)
	}
}

func TestIsScreenChanging_NilAndSizeMismatch(t *testing.T) {
	a := newSolidImage(100, 100, color.RGBA{50, 50, 50, 255})
	b := newSolidImage(120, 100, color.RGBA{50, 50, 50, 255})

	tests := []struct {
		name       string
		i1, i2     image.Image
		threshold  float64
		want       bool
	}{
		{"both nil", nil, nil, 0.1, true},
		{"nil left", nil, a, 0.1, true},
		{"nil right", a, nil, 0.1, true},
		{"size mismatch", a, b, 0.1, true},
	}
	for _, tt := range tests {
		if got := IsScreenChanging(tt.i1, tt.i2, tt.threshold); got != tt.want {
			t.Errorf("%s: IsScreenChanging = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestGetDominantColor_MixedRegion(t *testing.T) {
	// Half red, half blue in the sampled region: average should be
	// (~127, 0, ~127). Empty region returns zero value.
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	fillRect(img, 0, 0, 50, 100, color.RGBA{200, 0, 0, 255})
	fillRect(img, 50, 0, 50, 100, color.RGBA{0, 0, 200, 255})

	got := GetDominantColor(img, 0, 0, 100, 100)
	if got.R < 90 || got.R > 110 || got.B < 90 || got.B > 110 || got.G != 0 {
		t.Errorf("GetDominantColor(mixed) = %v, want ~(100,0,100)", got)
	}
	if got.A != 255 {
		t.Errorf("GetDominantColor alpha = %d, want 255", got.A)
	}

	zero := GetDominantColor(img, 0, 0, 0, 0)
	if zero != (color.RGBA{}) {
		t.Errorf("GetDominantColor(empty) = %v, want zero RGBA", zero)
	}
}
