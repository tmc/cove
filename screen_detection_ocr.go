// screen_detection_ocr.go - OCR-based screen state detection
package main

import (
	"image"
	"strings"
)

var helloContinueMarkers = []string{
	"continue", "continua", "continuar", "fortfahren", "fortsæt", "fortsett",
	"fortsätt", "ga door", "proceed", "продолжить",
}

// setupAssistantPageMarkers maps page names to their characteristic text.
var setupAssistantPageMarkers = map[string][]string{
	"language":          {"English", "Deutsch", "Francais"},
	"country_region":    {"Select Your Country", "Country or Region"},
	"accessibility":     {"Accessibility"},
	"wifi":              {"Wi-Fi", "Select a Wi-Fi Network"},
	"location_services": {"Location Services", "Enable Location Services"},
	"migration":         {"Migration Assistant", "Transfer Information"},
	"apple_id":          {"Apple ID", "Sign In with Your Apple"},
	"terms":             {"Terms and Conditions"},
	"user_account":      {"Create a Computer Account", "Create a Mac Account", "Full Name"},
	"express_setup":     {"Express Set Up"},
	"analytics":         {"Analytics", "Help Apple Improve"},
	"screen_time":       {"Screen Time"},
	"siri":              {"Siri"},
	"siri_voice":        {"Select a Siri Voice"},
	"siri_dictation":    {"Improve Siri & Dictation", "Improve Siri"},
	"time_zone":         {"Select Your Time Zone", "Time Zone"},
	"appearance":        {"Choose Your Look", "Light", "Dark", "Auto"},
	"touch_id":          {"Touch ID"},
	"filevault":         {"FileVault"},
	"update_mac":        {"Update Mac Automatically", "Keep Your Mac Up to Date"},
	"welcome":           {"Get Started"},
	"icloud_keychain":   {"iCloud Keychain"},
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

// ocrPageDetectionOrder defines the order in which pages are checked.
// More specific markers are checked before generic ones to avoid false matches.
// For example, "hello" is checked last because it's a common word.
var ocrPageDetectionOrder = []struct {
	page    string
	markers []string
}{
	{"user_account", []string{"create a computer account", "create a mac account", "full name", "account name"}},
	{"terms", []string{"terms and conditions"}},
	{"migration", []string{"migration assistant", "transfer information", "transfer your data", "data to this mac"}},
	{"apple_id", []string{"apple id", "sign in with your apple"}},
	{"country_region", []string{"select your country", "country or region"}},
	{"location_services", []string{"enable location services", "location services"}},
	{"time_zone", []string{"select your time zone"}},
	{"express_setup", []string{"express set up"}},
	{"analytics", []string{"help apple improve", "share mac analytics"}},
	{"screen_time", []string{"screen time"}},
	{"siri_voice", []string{"select a siri voice"}},
	{"siri_dictation", []string{"improve siri & dictation", "improve siri"}},
	{"siri", []string{"enable siri", "ask siri"}},
	{"appearance", []string{"choose your look"}},
	{"touch_id", []string{"touch id"}},
	{"filevault", []string{"filevault"}},
	{"update_mac", []string{"update mac automatically", "keep your mac up to date"}},
	{"icloud_keychain", []string{"icloud keychain"}},
	{"accessibility", []string{"accessibility"}},
	{"wifi", []string{"select a wi-fi network"}},
	{"privacy", []string{"data & privacy", "data and privacy"}},
	{"welcome", []string{"get started"}},
	{"language", []string{"select your language", "choose your language", "language", "idioma"}},
	{"hello", []string{"hello", "hollo", "bonjour"}},
}

// OCRDetectSetupAssistantPage uses OCR to identify the current Setup Assistant page.
// Returns a page name string (e.g., "language", "migration", "user_account")
// or "unknown" if no known page is detected.
func OCRDetectSetupAssistantPage(img image.Image, ocr *OCRService) string {
	if ocr == nil || img == nil {
		return "unknown"
	}

	text := ocr.AllText(img)
	if text == "" {
		return "unknown"
	}

	lower := strings.ToLower(text)

	// Check for desktop or login (not Setup Assistant pages)
	if strings.Contains(lower, "finder") && strings.Contains(lower, "file") {
		return "desktop"
	}
	if strings.Contains(lower, "enter password") || strings.Contains(lower, "login window") {
		return "login"
	}

	// Check pages in priority order
	for _, entry := range ocrPageDetectionOrder {
		for _, marker := range entry.markers {
			if strings.Contains(lower, marker) {
				return entry.page
			}
		}
	}

	// The multilingual Hello screen can OCR as a short greeting plus a translated
	// Continue button, without any stable "hello" token. If all we can reliably
	// see is a Continue-like button and no stronger page marker matched above,
	// treat it as the hello page so the dedicated handler can advance.
	if containsAny(lower, helloContinueMarkers) {
		return "hello"
	}

	return "unknown"
}

func containsAny(s string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(s, marker) {
			return true
		}
	}
	return false
}
