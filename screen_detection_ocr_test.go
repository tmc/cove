package main

import (
	"image"
	"image/color"
	"testing"
)

func TestDetectScreenStateOCR_NilOCR(t *testing.T) {
	// Should fall back to pixel-based detection when OCR is nil
	img := newSolidImage(1024, 768, color.RGBA{0, 0, 0, 255})
	state := DetectScreenStateOCR(img, nil)
	if state != ScreenStateBlack {
		t.Errorf("DetectScreenStateOCR(black, nil ocr) = %v, want %v", state, ScreenStateBlack)
	}
}

func TestDetectScreenStateOCR_NilImage(t *testing.T) {
	ocr := NewOCRService(false)
	state := DetectScreenStateOCR(nil, ocr)
	// nil image → pixel-based fallback → ScreenStateUnknown
	if state != ScreenStateUnknown {
		t.Errorf("DetectScreenStateOCR(nil, ocr) = %v, want %v", state, ScreenStateUnknown)
	}
}

func TestDetectScreenStateOCR_BlankImage(t *testing.T) {
	// Blank image → no text → falls back to pixel-based heuristics
	ocr := NewOCRService(false)
	img := newSolidImage(1024, 768, color.RGBA{0, 0, 0, 255})
	state := DetectScreenStateOCR(img, ocr)
	if state != ScreenStateBlack {
		t.Errorf("DetectScreenStateOCR(black) = %v, want %v", state, ScreenStateBlack)
	}
}

func TestSetupAssistantPageMarkers(t *testing.T) {
	// Verify all expected pages have markers defined
	expectedPages := []string{
		"language", "country_region", "accessibility", "wifi",
		"migration", "apple_id", "terms", "user_account",
		"express_setup", "analytics", "screen_time", "siri",
		"appearance", "touch_id", "filevault", "icloud_keychain",
	}

	for _, page := range expectedPages {
		markers, ok := setupAssistantPageMarkers[page]
		if !ok {
			t.Errorf("missing page markers for %q", page)
			continue
		}
		if len(markers) == 0 {
			t.Errorf("empty markers for page %q", page)
		}
	}
}

func TestDetectSetupAssistantPage_BlackScreen(t *testing.T) {
	img := newSolidImage(1024, 768, color.RGBA{0, 0, 0, 255})
	page := DetectSetupAssistantPage(img)
	// Very dark image → hello screen detection (centerBrightness 0, overall < 30)
	// Actually with pure black, center brightness is 0 too, so it won't match "hello"
	if page == "" {
		t.Error("expected non-empty page identifier")
	}
}

func TestScreenState_AllStatesHaveString(t *testing.T) {
	states := []ScreenState{
		ScreenStateUnknown,
		ScreenStateBlack,
		ScreenStateAppleLogo,
		ScreenStateSetupAssistant,
		ScreenStateLoginScreen,
		ScreenStateDesktop,
		ScreenStateRecoveryMode,
	}

	for _, s := range states {
		str := s.String()
		if str == "" {
			t.Errorf("ScreenState(%d).String() returned empty string", s)
		}
	}
}

func TestAnalyzeScreenRegions_EmptyImage(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 0, 0))
	// Should not panic on empty image
	_ = analyzeScreenRegions(img)
}

func TestHasDock_SmallImage(t *testing.T) {
	img := newSolidImage(100, 100, color.RGBA{128, 128, 128, 255})
	// Should not panic and should return false for uniform image
	if hasDock(img, 100, 100) {
		t.Error("expected no dock on uniform image")
	}
}

func TestCountInputFields_Uniform(t *testing.T) {
	img := newSolidImage(800, 600, color.RGBA{128, 128, 128, 255})
	count := countInputFields(img, 800, 600)
	if count != 0 {
		t.Errorf("expected 0 input fields on uniform image, got %d", count)
	}
}
