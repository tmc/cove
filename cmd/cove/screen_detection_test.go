package main

import (
	"image"
	"image/color"
	"testing"
)

// newSolidImage creates a test image filled with a solid color.
func newSolidImage(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func TestUniformImageHelpersReturnFalse(t *testing.T) {
	img := newSolidImage(800, 600, color.RGBA{128, 128, 128, 255})
	tests := []struct {
		name string
		got  bool
	}{
		{"hasLoginScreenElements", hasLoginScreenElements(img, 800, 600)},
		{"hasSetupAssistantButton", hasSetupAssistantButton(img, 800, 600)},
		{"hasAppleIDPattern", hasAppleIDPattern(img, 800, 600)},
		{"hasAppearancePattern", hasAppearancePattern(img, 800, 600)},
		{"hasMigrationPattern", hasMigrationPattern(img, 800, 600)},
		{"hasScreenTimePattern", hasScreenTimePattern(img, 800, 600)},
		{"hasHorizontalBands", hasHorizontalBands(img, 200, 400)},
	}
	for _, tt := range tests {
		if tt.got {
			t.Errorf("%s on uniform image = true, want false", tt.name)
		}
	}
	// hasPrivacyPattern treats uniform grayscale as a "lock" icon and returns true.
	if !hasPrivacyPattern(img, 800, 600) {
		t.Error("hasPrivacyPattern on uniform gray = false, want true")
	}
	// Saturated red breaks both isGray and isBlue.
	red := newSolidImage(800, 600, color.RGBA{255, 0, 0, 255})
	if hasPrivacyPattern(red, 800, 600) {
		t.Error("hasPrivacyPattern on red image = true, want false")
	}
}

func TestDetectScreenState_Nil(t *testing.T) {
	if got := DetectScreenState(nil); got != ScreenStateUnknown {
		t.Errorf("DetectScreenState(nil) = %v, want %v", got, ScreenStateUnknown)
	}
}

func TestDetectScreenState_Black(t *testing.T) {
	img := newSolidImage(1024, 768, color.RGBA{0, 0, 0, 255})
	got := DetectScreenState(img)
	if got != ScreenStateBlack {
		t.Errorf("DetectScreenState(black image) = %v, want %v", got, ScreenStateBlack)
	}
}

func TestDetectScreenState_VeryDark(t *testing.T) {
	// Very dark but not pure black — should still detect as black.
	img := newSolidImage(1024, 768, color.RGBA{5, 5, 5, 255})
	got := DetectScreenState(img)
	if got != ScreenStateBlack {
		t.Errorf("DetectScreenState(very dark image) = %v, want %v", got, ScreenStateBlack)
	}
}

func TestScreenState_String(t *testing.T) {
	tests := []struct {
		state ScreenState
		want  string
	}{
		{ScreenStateUnknown, "unknown"},
		{ScreenStateBlack, "black"},
		{ScreenStateAppleLogo, "apple_logo"},
		{ScreenStateSetupAssistant, "setup_assistant"},
		{ScreenStateLoginScreen, "login_screen"},
		{ScreenStateDesktop, "desktop"},
		{ScreenStateRecoveryMode, "recovery_mode"},
		{ScreenStateGDMLogin, "gdm_login"},
		{ScreenStateGNOMEDesktop, "gnome_desktop"},
		{ScreenStateGNOMEWelcome, "gnome_welcome"},
		{ScreenStateGRUBMenu, "grub_menu"},
	}

	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("ScreenState(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestIsScreenChanging(t *testing.T) {
	img1 := newSolidImage(100, 100, color.RGBA{100, 100, 100, 255})
	img2 := newSolidImage(100, 100, color.RGBA{100, 100, 100, 255})

	// Identical images should not be "changing".
	if IsScreenChanging(img1, img2, 0.1) {
		t.Error("IsScreenChanging(identical images) = true, want false")
	}

	// Very different images should be "changing".
	img3 := newSolidImage(100, 100, color.RGBA{255, 255, 255, 255})
	if !IsScreenChanging(img1, img3, 0.1) {
		t.Error("IsScreenChanging(very different images) = false, want true")
	}
}

func TestGetDominantColor(t *testing.T) {
	red := color.RGBA{255, 0, 0, 255}
	img := newSolidImage(100, 100, red)

	got := GetDominantColor(img, 0, 0, 50, 50)
	if got.R != 255 || got.G != 0 || got.B != 0 {
		t.Errorf("GetDominantColor(red image) = %v, want pure red", got)
	}
}
