package main

import (
	"image"
	"image/color"
	"testing"

	ocrx "github.com/tmc/apple/x/vzkit/ocr"
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
	ocr := ocrx.NewService(false)
	state := DetectScreenStateOCR(nil, ocr)
	// nil image → pixel-based fallback → ScreenStateUnknown
	if state != ScreenStateUnknown {
		t.Errorf("DetectScreenStateOCR(nil, ocr) = %v, want %v", state, ScreenStateUnknown)
	}
}

func TestDetectScreenStateOCR_BlankImage(t *testing.T) {
	// Blank image → no text → falls back to pixel-based heuristics
	ocr := ocrx.NewService(false)
	img := newSolidImage(1024, 768, color.RGBA{0, 0, 0, 255})
	state := DetectScreenStateOCR(img, ocr)
	if state != ScreenStateBlack {
		t.Errorf("DetectScreenStateOCR(black) = %v, want %v", state, ScreenStateBlack)
	}
}

func TestSetupAssistantPageMarkers(t *testing.T) {
	// Verify all expected pages have markers defined
	expectedPages := []string{
		"language", "country_region", "voiceover_tutorial", "accessibility", "wifi",
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
		ScreenStateGDMLogin,
		ScreenStateGNOMEDesktop,
		ScreenStateGNOMEWelcome,
		ScreenStateGRUBMenu,
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

func TestOCRDetectSetupAssistantPage_NilInputs(t *testing.T) {
	ocr := ocrx.NewService(false)

	if page := OCRDetectSetupAssistantPage(nil, ocr); page != "unknown" {
		t.Errorf("OCRDetectSetupAssistantPage(nil, ocr) = %q, want %q", page, "unknown")
	}

	img := newSolidImage(800, 600, color.RGBA{128, 128, 128, 255})
	if page := OCRDetectSetupAssistantPage(img, nil); page != "unknown" {
		t.Errorf("OCRDetectSetupAssistantPage(img, nil) = %q, want %q", page, "unknown")
	}
}

func TestOCRDetectSetupAssistantPage_BlankImage(t *testing.T) {
	ocr := ocrx.NewService(false)
	img := newSolidImage(800, 600, color.RGBA{255, 255, 255, 255})
	page := OCRDetectSetupAssistantPage(img, ocr)
	// Blank white image has no text, should return "unknown"
	if page != "unknown" {
		t.Errorf("OCRDetectSetupAssistantPage(blank) = %q, want %q", page, "unknown")
	}
}

func TestOCRPageDetectionOrder_HasEntries(t *testing.T) {
	if len(ocrPageDetectionOrder) == 0 {
		t.Fatal("ocrPageDetectionOrder is empty")
	}
	// Verify all entries have at least one marker
	for _, entry := range ocrPageDetectionOrder {
		if entry.page == "" {
			t.Error("empty page name in ocrPageDetectionOrder")
		}
		if len(entry.markers) == 0 {
			t.Errorf("no markers for page %q in ocrPageDetectionOrder", entry.page)
		}
	}
}

func TestDetectSetupAssistantPageFromOCRText(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{
			name: "voiceover tutorial wins over accessibility footer text",
			text: "VoiceOver Tutorial\nThe VoiceOver Modifier\nPress Command-Option-F5 to view accessibility options.",
			want: "voiceover_tutorial",
		},
		{
			name: "generic accessibility page remains accessibility",
			text: "Accessibility\nUse accessibility options to set up your Mac.",
			want: "accessibility",
		},
		{
			name: "country region is detected",
			text: "Select Your Country or Region\nUnited States\nContinue",
			want: "country_region",
		},
	}

	for _, tt := range tests {
		if got := detectSetupAssistantPageFromOCRText(tt.text); got != tt.want {
			t.Errorf("%s: detectSetupAssistantPageFromOCRText() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestDetectScreenStateFromOCRText(t *testing.T) {
	got := detectScreenStateFromOCRText("VoiceOver Tutorial\nThe VoiceOver Modifier")
	if got != ScreenStateSetupAssistant {
		t.Fatalf("detectScreenStateFromOCRText(voiceover tutorial) = %v, want %v", got, ScreenStateSetupAssistant)
	}
}

func TestDetectLinuxScreenStateFromOCRText(t *testing.T) {
	tests := []struct {
		name string
		text string
		want ScreenState
		page string
	}{
		{
			name: "gdm login",
			text: "Ubuntu\nPassword\nSign In\nNot listed?",
			want: ScreenStateGDMLogin,
			page: "gdm_login",
		},
		{
			name: "gnome desktop",
			text: "Activities\nTerminal\nFiles\nShow Applications",
			want: ScreenStateGNOMEDesktop,
			page: "gnome_desktop",
		},
		{
			name: "gnome welcome",
			text: "Welcome to Ubuntu\nOnline Accounts\nPrivacy\nReady to Go\nStart Using Ubuntu",
			want: ScreenStateGNOMEWelcome,
			page: "gnome_welcome",
		},
		{
			name: "grub menu",
			text: "GNU GRUB version 2.12\nUbuntu\nAdvanced options for Ubuntu\nUse the ↑ and ↓ keys to select which entry is highlighted.",
			want: ScreenStateGRUBMenu,
			page: "grub_menu",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := detectScreenStateFromOCRText(tt.text); got != tt.want {
				t.Fatalf("detectScreenStateFromOCRText() = %v, want %v", got, tt.want)
			}
			if got := detectSetupAssistantPageFromOCRText(tt.text); got != tt.page {
				t.Fatalf("detectSetupAssistantPageFromOCRText() = %q, want %q", got, tt.page)
			}
		})
	}
}
