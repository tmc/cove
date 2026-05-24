// screen_detection.go - Detect current guest UI state from screenshots

package main

import (
	"image"
	"image/color"
)

// ScreenState represents the detected UI state
type ScreenState int

const (
	ScreenStateUnknown        ScreenState = iota
	ScreenStateBlack                      // Black screen (booting or off)
	ScreenStateAppleLogo                  // Apple logo during boot
	ScreenStateSetupAssistant             // Setup Assistant (first-run experience)
	ScreenStateLoginScreen                // Login screen
	ScreenStateDesktop                    // Desktop with dock
	ScreenStateRecoveryMode               // Recovery mode
	ScreenStateGDMLogin                   // Linux GDM login screen
	ScreenStateGNOMEDesktop               // Linux GNOME desktop
	ScreenStateGNOMEWelcome               // Linux GNOME Initial Setup welcome
	ScreenStateGRUBMenu                   // Linux GRUB boot menu
)

func (s ScreenState) String() string {
	switch s {
	case ScreenStateBlack:
		return "black"
	case ScreenStateAppleLogo:
		return "apple_logo"
	case ScreenStateSetupAssistant:
		return "setup_assistant"
	case ScreenStateLoginScreen:
		return "login_screen"
	case ScreenStateDesktop:
		return "desktop"
	case ScreenStateRecoveryMode:
		return "recovery_mode"
	case ScreenStateGDMLogin:
		return "gdm_login"
	case ScreenStateGNOMEDesktop:
		return "gnome_desktop"
	case ScreenStateGNOMEWelcome:
		return "gnome_welcome"
	case ScreenStateGRUBMenu:
		return "grub_menu"
	default:
		return "unknown"
	}
}

// DetectScreenState analyzes a screenshot to determine the current UI state
func DetectScreenState(img image.Image) ScreenState {
	if img == nil {
		return ScreenStateUnknown
	}

	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	// Analyze different regions of the screen
	stats := analyzeScreenRegions(img)

	// Black screen detection
	if stats.overallBrightness < 10 {
		return ScreenStateBlack
	}

	// Apple logo detection - dark background with lighter center
	if stats.overallBrightness < 30 && stats.centerBrightness > stats.cornerBrightness*1.5 {
		return ScreenStateAppleLogo
	}

	// Check for dock at bottom (indicates desktop)
	if hasDock(img, width, height) {
		return ScreenStateDesktop
	}

	// Check for Setup Assistant first - it has a centered button in the lower area
	// Modern macOS Setup Assistant uses the same scenic wallpapers as login screen
	if hasSetupAssistantButton(img, width, height) {
		return ScreenStateSetupAssistant
	}

	// Login screen detection - check for the distinctive login UI elements
	// The login screen has:
	// 1. No dock
	// 2. User avatar area at bottom center (darker/lighter than surroundings)
	// 3. Often has greeting text in upper-center area
	if hasLoginScreenElements(img, width, height) {
		return ScreenStateLoginScreen
	}

	// Setup Assistant typically has a dark/gradient background with centered content
	// and is more colorful than login screen
	if stats.hasGradientBackground && stats.colorfulness > 15 {
		return ScreenStateSetupAssistant
	}

	// Login screen with standard macOS wallpaper (lighter colors)
	if stats.overallBrightness > 150 && stats.topBrightness > stats.bottomBrightness {
		return ScreenStateLoginScreen
	}

	// High colorfulness with no dock - could be either login or setup assistant
	// Default to setup assistant since that's what we're typically looking for during provisioning
	if stats.colorfulness > 50 && stats.overallBrightness > 25 && stats.overallBrightness < 200 {
		return ScreenStateSetupAssistant
	}

	// Setup assistant has darker top and is moderately bright
	if stats.overallBrightness > 40 && stats.overallBrightness < 150 {
		return ScreenStateSetupAssistant
	}

	return ScreenStateUnknown
}

// hasLoginScreenElements checks for login screen UI elements
// The login screen in macOS shows user avatars in the lower portion of the screen
// and often has a greeting text in the upper-center
func hasLoginScreenElements(img image.Image, width, height int) bool {
	// Check for the user selection area (bottom third of screen, centered)
	// This area typically has a lighter/rounded rectangle for user avatars
	bottomThird := height * 2 / 3
	centerX := width / 2
	sampleWidth := width / 5 // Sample 20% width around center

	var loginAreaBrightness, aboveAreaBrightness float64
	var loginCount, aboveCount int

	// Sample the login area (bottom center)
	for y := bottomThird; y < height-height/10; y += 5 {
		for x := centerX - sampleWidth; x < centerX+sampleWidth; x += 5 {
			if x >= 0 && x < width && y >= 0 && y < height {
				c := img.At(x, y)
				r, g, b, _ := c.RGBA()
				brightness := float64(uint8(r>>8)+uint8(g>>8)+uint8(b>>8)) / 3.0
				loginAreaBrightness += brightness
				loginCount++
			}
		}
	}

	// Sample area above the login area
	for y := height / 3; y < bottomThird; y += 5 {
		for x := centerX - sampleWidth; x < centerX+sampleWidth; x += 5 {
			if x >= 0 && x < width && y >= 0 && y < height {
				c := img.At(x, y)
				r, g, b, _ := c.RGBA()
				brightness := float64(uint8(r>>8)+uint8(g>>8)+uint8(b>>8)) / 3.0
				aboveAreaBrightness += brightness
				aboveCount++
			}
		}
	}

	if loginCount > 0 && aboveCount > 0 {
		avgLogin := loginAreaBrightness / float64(loginCount)
		avgAbove := aboveAreaBrightness / float64(aboveCount)

		// Login screen typically has a user panel that's different brightness than background
		// The difference can be either lighter (glass effect) or darker (dark mode)
		brightnessDiff := abs(avgLogin - avgAbove)
		if brightnessDiff > 10 {
			return true
		}
	}

	return false
}

// hasSetupAssistantButton checks for the distinctive centered button of Setup Assistant
// Setup Assistant has a pill-shaped "Continue" button in the lower-center area
// The button is lighter/different than the surrounding wallpaper
func hasSetupAssistantButton(img image.Image, width, height int) bool {
	// The button in Setup Assistant is typically in the lower third, centered
	// It's a small, pill-shaped button (about 100-200px wide)
	buttonY := height * 3 / 4 // Check around 75% down
	buttonHeight := height / 10
	buttonWidth := width / 6
	centerX := width / 2

	// Sample the button area (should be lighter - white/gray button)
	var buttonBrightness, surroundBrightness float64
	var buttonCount, surroundCount int

	// Sample the button region
	for y := buttonY; y < buttonY+buttonHeight && y < height; y += 3 {
		for x := centerX - buttonWidth/2; x < centerX+buttonWidth/2 && x < width; x += 3 {
			if x >= 0 && y >= 0 {
				c := img.At(x, y)
				r, g, b, _ := c.RGBA()
				brightness := float64(uint8(r>>8)+uint8(g>>8)+uint8(b>>8)) / 3.0
				buttonBrightness += brightness
				buttonCount++
			}
		}
	}

	// Sample surrounding area (left and right of button)
	for y := buttonY; y < buttonY+buttonHeight && y < height; y += 3 {
		// Left side
		for x := centerX - buttonWidth; x < centerX-buttonWidth/2; x += 3 {
			if x >= 0 && y >= 0 {
				c := img.At(x, y)
				r, g, b, _ := c.RGBA()
				brightness := float64(uint8(r>>8)+uint8(g>>8)+uint8(b>>8)) / 3.0
				surroundBrightness += brightness
				surroundCount++
			}
		}
		// Right side
		for x := centerX + buttonWidth/2; x < centerX+buttonWidth && x < width; x += 3 {
			if x >= 0 && y >= 0 {
				c := img.At(x, y)
				r, g, b, _ := c.RGBA()
				brightness := float64(uint8(r>>8)+uint8(g>>8)+uint8(b>>8)) / 3.0
				surroundBrightness += brightness
				surroundCount++
			}
		}
	}

	if buttonCount > 0 && surroundCount > 0 {
		avgButton := buttonBrightness / float64(buttonCount)
		avgSurround := surroundBrightness / float64(surroundCount)

		// Setup Assistant button is typically lighter than surroundings (white/light gray)
		// A difference of 30+ suggests a visible button
		if avgButton > avgSurround+30 {
			return true
		}
	}

	return false
}

// screenStats holds analysis data about different screen regions
type screenStats struct {
	overallBrightness     float64
	centerBrightness      float64
	cornerBrightness      float64
	topBrightness         float64
	bottomBrightness      float64
	hasGradientBackground bool
	colorfulness          float64
}

// DebugScreenStats holds debug information about screen analysis
type DebugScreenStats struct {
	OverallBrightness float64
	CenterBrightness  float64
	CornerBrightness  float64
	TopBrightness     float64
	BottomBrightness  float64
	Colorfulness      float64
	HasGradient       bool
	HasDock           bool
	HasLoginElements  bool
}

// DebugScreenAnalysis returns detailed analysis of the screen for debugging
func DebugScreenAnalysis(img image.Image) DebugScreenStats {
	if img == nil {
		return DebugScreenStats{}
	}

	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	stats := analyzeScreenRegions(img)

	return DebugScreenStats{
		OverallBrightness: stats.overallBrightness,
		CenterBrightness:  stats.centerBrightness,
		CornerBrightness:  stats.cornerBrightness,
		TopBrightness:     stats.topBrightness,
		BottomBrightness:  stats.bottomBrightness,
		Colorfulness:      stats.colorfulness,
		HasGradient:       stats.hasGradientBackground,
		HasDock:           hasDock(img, width, height),
		HasLoginElements:  hasLoginScreenElements(img, width, height),
	}
}

// analyzeScreenRegions computes statistics about different screen regions
func analyzeScreenRegions(img image.Image) screenStats {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	var stats screenStats

	// Sample points for analysis (avoid every pixel for performance)
	sampleStep := 10

	var totalBright, centerBright, cornerBright, topBright, bottomBright float64
	var totalCount, centerCount, cornerCount, topCount, bottomCount int
	var totalColorfulness float64

	centerX, centerY := width/2, height/2
	centerRadius := minInt(width, height) / 4

	for y := bounds.Min.Y; y < bounds.Max.Y; y += sampleStep {
		for x := bounds.Min.X; x < bounds.Max.X; x += sampleStep {
			c := img.At(x, y)
			r, g, b, _ := c.RGBA()
			r8, g8, b8 := uint8(r>>8), uint8(g>>8), uint8(b>>8)

			brightness := float64(r8+g8+b8) / 3.0

			// Overall brightness
			totalBright += brightness
			totalCount++

			// Colorfulness (max - min channel difference)
			maxC := maxUint8(r8, maxUint8(g8, b8))
			minC := minUint8(r8, minUint8(g8, b8))
			totalColorfulness += float64(maxC - minC)

			// Distance from center
			dx := x - centerX
			dy := y - centerY
			distFromCenter := dx*dx + dy*dy

			// Center region
			if distFromCenter < centerRadius*centerRadius {
				centerBright += brightness
				centerCount++
			}

			// Corner regions (within 10% of edges)
			cornerDist := width / 10
			isCorner := (x < cornerDist || x > width-cornerDist) && (y < cornerDist || y > height-cornerDist)
			if isCorner {
				cornerBright += brightness
				cornerCount++
			}

			// Top and bottom regions
			if y < height/3 {
				topBright += brightness
				topCount++
			} else if y > 2*height/3 {
				bottomBright += brightness
				bottomCount++
			}
		}
	}

	if totalCount > 0 {
		stats.overallBrightness = totalBright / float64(totalCount)
		stats.colorfulness = totalColorfulness / float64(totalCount)
	}
	if centerCount > 0 {
		stats.centerBrightness = centerBright / float64(centerCount)
	}
	if cornerCount > 0 {
		stats.cornerBrightness = cornerBright / float64(cornerCount)
	}
	if topCount > 0 {
		stats.topBrightness = topBright / float64(topCount)
	}
	if bottomCount > 0 {
		stats.bottomBrightness = bottomBright / float64(bottomCount)
	}

	// Check for gradient (top-to-bottom brightness change)
	stats.hasGradientBackground = abs(stats.topBrightness-stats.bottomBrightness) > 20

	return stats
}

// hasDock checks if there's a dock at the bottom of the screen
// The dock appears as a semi-transparent bar with distinct icons
func hasDock(img image.Image, width, height int) bool {
	// Check the bottom strip of the screen (about 5% from bottom)
	dockHeight := height / 20
	startY := height - dockHeight

	// The dock has a distinctive pattern: it's slightly lighter than surroundings
	// and has a consistent horizontal band
	var dockBrightness, aboveBrightness float64
	var dockCount, aboveCount int

	sampleStep := 5

	// Sample dock region
	for y := startY; y < height; y += sampleStep {
		for x := width / 4; x < 3*width/4; x += sampleStep {
			c := img.At(x, y)
			r, g, b, _ := c.RGBA()
			brightness := float64(r>>8+g>>8+b>>8) / 3.0
			dockBrightness += brightness
			dockCount++
		}
	}

	// Sample region just above dock
	aboveStartY := startY - dockHeight*2
	for y := aboveStartY; y < startY; y += sampleStep {
		for x := width / 4; x < 3*width/4; x += sampleStep {
			c := img.At(x, y)
			r, g, b, _ := c.RGBA()
			brightness := float64(r>>8+g>>8+b>>8) / 3.0
			aboveBrightness += brightness
			aboveCount++
		}
	}

	if dockCount == 0 || aboveCount == 0 {
		return false
	}

	avgDock := dockBrightness / float64(dockCount)
	avgAbove := aboveBrightness / float64(aboveCount)

	// Dock typically has a translucent appearance with a brightness difference
	// from the content above it
	brightnessDiff := abs(avgDock - avgAbove)
	return brightnessDiff > 10 && avgDock > 20
}

// DetectSetupAssistantPage tries to identify which Setup Assistant page is displayed
// Returns a string identifier for the page
func DetectSetupAssistantPage(img image.Image) string {
	// This is a simplified detection that looks at screen characteristics
	// In practice, more sophisticated detection (OCR) might be needed

	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	stats := analyzeScreenRegions(img)

	// "Hello" screen has very dark background with centered bright text
	if stats.overallBrightness < 30 && stats.centerBrightness > 50 {
		return "hello"
	}

	// Language selection - similar to hello but with list visible
	if stats.overallBrightness < 40 && stats.centerBrightness > 30 {
		// Check for list-like pattern in center
		if hasHorizontalBands(img, height/3, 2*height/3) {
			return "language"
		}
	}

	// Country selection has a list view (moderate overall brightness, structured layout)
	if stats.overallBrightness > 50 && stats.overallBrightness < 120 {
		// Check for scrollable list pattern
		if hasHorizontalBands(img, height/4, 3*height/4) {
			return "country_region"
		}
	}

	// Check for input fields (white rectangles) suggesting user account page
	if stats.overallBrightness > 80 {
		inputFieldCount := countInputFields(img, width, height)
		if inputFieldCount >= 3 {
			return "user_account"
		}
	}

	// Terms and conditions - look for long scrollable text area
	if stats.overallBrightness > 100 && stats.overallBrightness < 180 {
		if hasScrollableTextArea(img, width, height) {
			return "terms"
		}
	}

	// Apple ID / Sign In screen - typically has Apple logo pattern
	if stats.overallBrightness > 60 && stats.overallBrightness < 140 {
		if hasAppleIDPattern(img, width, height) {
			return "apple_id"
		}
	}

	// Express setup - typically has checkbox patterns
	if stats.overallBrightness > 100 {
		checkboxCount := countCheckboxes(img, width, height)
		if checkboxCount >= 2 {
			// Could be express_setup, analytics, or privacy
			// Distinguish based on layout
			if stats.topBrightness > stats.bottomBrightness*1.2 {
				return "express_setup"
			}
			return "analytics"
		}
	}

	// Appearance/Choose Look - three distinct regions for light/dark/auto
	if stats.overallBrightness > 80 && stats.overallBrightness < 160 {
		if hasAppearancePattern(img, width, height) {
			return "choose_look"
		}
	}

	// Migration Assistant - check for icon pattern
	if stats.overallBrightness > 100 && stats.overallBrightness < 170 {
		if hasMigrationPattern(img, width, height) {
			return "migration"
		}
	}

	// Accessibility page - specific layout with accessibility icon
	if stats.overallBrightness > 60 && stats.overallBrightness < 130 {
		if stats.colorfulness < 20 { // Typically grayscale-ish
			return "accessibility"
		}
	}

	// Siri - typically has blue Siri waveform/icon
	if stats.colorfulness > 25 {
		if hasBlueAccent(img, width, height) {
			return "siri"
		}
	}

	// Screen Time - check for specific layout
	if stats.overallBrightness > 120 && stats.overallBrightness < 180 {
		if hasScreenTimePattern(img, width, height) {
			return "screen_time"
		}
	}

	// Privacy - usually has a lock/shield icon
	if stats.overallBrightness > 100 {
		if hasPrivacyPattern(img, width, height) {
			return "privacy"
		}
	}

	// User account creation page typically has input fields
	// (lighter overall with form elements) - fallback check
	if stats.overallBrightness > 100 {
		return "user_account"
	}

	return "unknown"
}

// countInputFields counts potential input field regions (white/light rectangles)
func countInputFields(img image.Image, width, height int) int {
	count := 0
	// Sample center column looking for horizontal light bands (input fields)
	centerX := width / 2
	fieldWidth := width / 4

	inField := false
	for y := height / 4; y < 3*height/4; y += 3 {
		// Sample across the center region
		lightCount := 0
		for x := centerX - fieldWidth; x < centerX+fieldWidth; x += 10 {
			c := img.At(x, y)
			r, g, b, _ := c.RGBA()
			brightness := (r>>8 + g>>8 + b>>8) / 3
			if brightness > 230 { // Very light (input field)
				lightCount++
			}
		}

		isFieldRow := lightCount > (fieldWidth/10)/2
		if isFieldRow && !inField {
			count++
			inField = true
		} else if !isFieldRow {
			inField = false
		}
	}
	return count
}

// hasScrollableTextArea checks for a large text area (terms & conditions)
func hasScrollableTextArea(img image.Image, width, height int) bool {
	// Look for a tall rectangular region with consistent light color
	centerX := width / 2
	textWidth := width / 3

	consistentRows := 0
	for y := height / 4; y < 3*height/4; y += 5 {
		lightCount := 0
		for x := centerX - textWidth; x < centerX+textWidth; x += 10 {
			c := img.At(x, y)
			r, g, b, _ := c.RGBA()
			brightness := (r>>8 + g>>8 + b>>8) / 3
			if brightness > 200 {
				lightCount++
			}
		}
		if lightCount > (textWidth*2/10)/2 {
			consistentRows++
		}
	}

	// Terms page typically has a large scrollable area
	threshold := (height / 2) / 5 * 7 / 10 // 70% of expected rows
	return consistentRows > threshold
}

// hasAppleIDPattern checks for Apple ID sign-in page characteristics
func hasAppleIDPattern(img image.Image, width, height int) bool {
	// Apple ID page typically has a centered icon/logo area in upper portion
	// and input fields below
	centerY := height / 3
	centerX := width / 2

	// Check for distinct icon region in upper center
	iconRegion := GetDominantColor(img, centerX-50, centerY-50, 100, 100)
	if iconRegion.R > 200 && iconRegion.G > 200 && iconRegion.B > 200 {
		// Light icon region - might be Apple ID page
		return true
	}

	return false
}

// countCheckboxes estimates number of checkbox-like elements
func countCheckboxes(img image.Image, width, height int) int {
	count := 0
	// Checkboxes are typically small squares with distinct borders
	// Look for them in the center-left region

	checkboxSize := 20
	searchStartX := width / 4
	searchEndX := width / 2

	for y := height / 3; y < 2*height/3; y += 30 {
		for x := searchStartX; x < searchEndX; x += 30 {
			if looksLikeCheckbox(img, x, y, checkboxSize) {
				count++
			}
		}
	}
	return count
}

// looksLikeCheckbox checks if a region looks like a checkbox
func looksLikeCheckbox(img image.Image, x, y, size int) bool {
	// Check for a small bordered square
	// Border pixels should be darker than interior

	edgeBrightness := float64(0)
	interiorBrightness := float64(0)
	edgeCount := 0
	interiorCount := 0

	for py := y; py < y+size; py++ {
		for px := x; px < x+size; px++ {
			c := img.At(px, py)
			r, g, b, _ := c.RGBA()
			brightness := float64(r>>8+g>>8+b>>8) / 3

			isEdge := px == x || px == x+size-1 || py == y || py == y+size-1
			if isEdge {
				edgeBrightness += brightness
				edgeCount++
			} else {
				interiorBrightness += brightness
				interiorCount++
			}
		}
	}

	if edgeCount == 0 || interiorCount == 0 {
		return false
	}

	avgEdge := edgeBrightness / float64(edgeCount)
	avgInterior := interiorBrightness / float64(interiorCount)

	// Checkbox has darker edge than interior, or is all similar (filled)
	return avgInterior > avgEdge+20 || (avgEdge > 100 && avgInterior > 100)
}

// hasAppearancePattern checks for light/dark/auto appearance selection
func hasAppearancePattern(img image.Image, width, height int) bool {
	// Three distinct regions horizontally in center
	centerY := height / 2
	sectionWidth := width / 5

	// Sample three horizontal sections
	var brightness [3]float64
	for i := 0; i < 3; i++ {
		x := width/4 + i*sectionWidth
		c := GetDominantColor(img, x, centerY-30, sectionWidth/2, 60)
		brightness[i] = float64(c.R+c.G+c.B) / 3
	}

	// Should have distinct brightness values (light, dark, auto)
	diff1 := abs(brightness[0] - brightness[1])
	diff2 := abs(brightness[1] - brightness[2])

	return diff1 > 30 || diff2 > 30
}

// hasMigrationPattern checks for Migration Assistant page
func hasMigrationPattern(img image.Image, width, height int) bool {
	// Migration typically has an icon in upper center
	// and "Not Now" option visible
	centerX := width / 2
	topY := height / 4

	// Check for distinct colored region (migration icon)
	iconColor := GetDominantColor(img, centerX-40, topY-40, 80, 80)
	brightness := float64(iconColor.R+iconColor.G+iconColor.B) / 3

	// Icon is typically distinct from background
	return brightness > 100 && brightness < 220
}

// hasBlueAccent checks for blue-colored elements (Siri)
func hasBlueAccent(img image.Image, width, height int) bool {
	// Siri has characteristic blue/purple waveform
	blueCount := 0
	sampleStep := 20

	for y := height / 3; y < 2*height/3; y += sampleStep {
		for x := width / 4; x < 3*width/4; x += sampleStep {
			c := img.At(x, y)
			r, g, b, _ := c.RGBA()
			r8, g8, b8 := r>>8, g>>8, b>>8

			// Check for blue-ish color
			if b8 > r8+30 && b8 > g8 {
				blueCount++
			}
		}
	}

	totalSamples := ((2*height/3 - height/3) / sampleStep) * ((3*width/4 - width/4) / sampleStep)
	return blueCount > totalSamples/10
}

// hasScreenTimePattern checks for Screen Time setup page
func hasScreenTimePattern(img image.Image, width, height int) bool {
	// Screen Time typically has a graph/chart icon
	// Look for colorful icon in center-top
	centerX := width / 2
	topY := height / 4

	// Sample colorfulness in icon region
	var colorfulness float64
	count := 0
	for y := topY - 30; y < topY+30; y += 5 {
		for x := centerX - 30; x < centerX+30; x += 5 {
			c := img.At(x, y)
			r, g, b, _ := c.RGBA()
			r8, g8, b8 := uint8(r>>8), uint8(g>>8), uint8(b>>8)
			maxC := maxUint8(r8, maxUint8(g8, b8))
			minC := minUint8(r8, minUint8(g8, b8))
			colorfulness += float64(maxC - minC)
			count++
		}
	}

	if count > 0 {
		avgColorfulness := colorfulness / float64(count)
		return avgColorfulness > 30 // Colorful icon
	}
	return false
}

// hasPrivacyPattern checks for privacy/security page
func hasPrivacyPattern(img image.Image, width, height int) bool {
	// Privacy pages often have lock/shield icon
	// These are typically grayscale or blue
	centerX := width / 2
	topY := height / 4

	iconColor := GetDominantColor(img, centerX-30, topY-30, 60, 60)

	// Check for grayscale (lock) or blue (shield)
	isGray := abs(float64(iconColor.R)-float64(iconColor.G)) < 20 &&
		abs(float64(iconColor.G)-float64(iconColor.B)) < 20
	isBlue := iconColor.B > iconColor.R+20

	return isGray || isBlue
}

// hasHorizontalBands checks if there are horizontal bands (list items) in a region
func hasHorizontalBands(img image.Image, startY, endY int) bool {
	bounds := img.Bounds()
	width := bounds.Dx()

	// Sample the center column and look for brightness transitions
	centerX := width / 2
	transitions := 0
	var lastBrightness float64

	for y := startY; y < endY; y += 5 {
		c := img.At(centerX, y)
		r, g, b, _ := c.RGBA()
		brightness := float64(r>>8+g>>8+b>>8) / 3.0

		if y > startY && abs(brightness-lastBrightness) > 20 {
			transitions++
		}
		lastBrightness = brightness
	}

	// Multiple transitions suggest a list
	return transitions > 3
}

// IsScreenChanging compares two screenshots to detect if the screen is changing
func IsScreenChanging(img1, img2 image.Image, threshold float64) bool {
	if img1 == nil || img2 == nil {
		return true
	}

	bounds1 := img1.Bounds()
	bounds2 := img2.Bounds()

	// Different sizes = definitely changing
	if bounds1.Dx() != bounds2.Dx() || bounds1.Dy() != bounds2.Dy() {
		return true
	}

	width, height := bounds1.Dx(), bounds1.Dy()
	sampleStep := 20

	var totalDiff float64
	var count int

	for y := bounds1.Min.Y; y < bounds1.Max.Y; y += sampleStep {
		for x := bounds1.Min.X; x < bounds1.Max.X; x += sampleStep {
			c1 := img1.At(x, y)
			c2 := img2.At(x, y)

			r1, g1, b1, _ := c1.RGBA()
			r2, g2, b2, _ := c2.RGBA()

			diff := abs(float64(r1>>8)-float64(r2>>8)) +
				abs(float64(g1>>8)-float64(g2>>8)) +
				abs(float64(b1>>8)-float64(b2>>8))

			totalDiff += diff
			count++
		}
	}

	if count == 0 {
		return false
	}

	avgDiff := totalDiff / float64(count)
	percentChange := avgDiff / 765.0 * 100 // 765 = 255 * 3 (max possible diff)

	_ = width
	_ = height

	return percentChange > threshold
}

// Helper for absolute value
func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

// GetDominantColor returns the dominant color in an image region
func GetDominantColor(img image.Image, x, y, w, h int) color.RGBA {
	var totalR, totalG, totalB uint64
	var count uint64

	for py := y; py < y+h; py++ {
		for px := x; px < x+w; px++ {
			c := img.At(px, py)
			r, g, b, _ := c.RGBA()
			totalR += uint64(r >> 8)
			totalG += uint64(g >> 8)
			totalB += uint64(b >> 8)
			count++
		}
	}

	if count == 0 {
		return color.RGBA{}
	}

	return color.RGBA{
		R: uint8(totalR / count),
		G: uint8(totalG / count),
		B: uint8(totalB / count),
		A: 255,
	}
}

// minInt helper for int
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// minUint8 helper for uint8
func minUint8(a, b uint8) uint8 {
	if a < b {
		return a
	}
	return b
}

// maxUint8 helper for uint8
func maxUint8(a, b uint8) uint8 {
	if a > b {
		return a
	}
	return b
}
