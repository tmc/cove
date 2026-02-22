// screen_detection_ocr.go - OCR-based screen state detection
package main

import (
	"image"
	"strings"
)

// setupAssistantPageMarkers maps page names to their characteristic text.
var setupAssistantPageMarkers = map[string][]string{
	"language":        {"English", "Deutsch", "Francais"},
	"country_region":  {"Select Your Country", "Country or Region"},
	"accessibility":   {"Accessibility"},
	"wifi":            {"Wi-Fi", "Select a Wi-Fi Network"},
	"migration":       {"Migration Assistant", "Transfer Information"},
	"apple_id":        {"Apple ID", "Sign In with Your Apple"},
	"terms":           {"Terms and Conditions"},
	"user_account":    {"Create a Computer Account", "Full Name"},
	"express_setup":   {"Express Set Up"},
	"analytics":       {"Analytics", "Help Apple Improve"},
	"screen_time":     {"Screen Time"},
	"siri":            {"Siri"},
	"appearance":      {"Choose Your Look", "Light", "Dark", "Auto"},
	"touch_id":        {"Touch ID"},
	"filevault":       {"FileVault"},
	"icloud_keychain": {"iCloud Keychain"},
}

// DetectScreenStateOCR uses OCR to determine the current screen state.
// Falls back to pixel-based detection if OCR doesn't match.
func DetectScreenStateOCR(img image.Image, ocr *OCRService) ScreenState {
	if ocr == nil {
		return DetectScreenState(img)
	}

	text := ocr.AllText(img)
	if text == "" {
		// No text found — use pixel heuristics
		return DetectScreenState(img)
	}

	lower := strings.ToLower(text)

	// Desktop detection — look for Finder menu bar text
	if strings.Contains(lower, "finder") && strings.Contains(lower, "file") {
		return ScreenStateDesktop
	}

	// Login screen — look for password field indicator
	if strings.Contains(lower, "enter password") || strings.Contains(lower, "login window") {
		return ScreenStateLoginScreen
	}

	// Recovery mode
	if strings.Contains(lower, "recovery") && strings.Contains(lower, "reinstall") {
		return ScreenStateRecoveryMode
	}

	// Setup Assistant — check for any known page marker
	for _, markers := range setupAssistantPageMarkers {
		for _, marker := range markers {
			if strings.Contains(lower, strings.ToLower(marker)) {
				return ScreenStateSetupAssistant
			}
		}
	}

	// Fall back to pixel heuristics
	return DetectScreenState(img)
}
