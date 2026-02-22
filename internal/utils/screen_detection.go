// screen_detection.go - Detect current macOS UI state from screenshots
package utils

import (
	"image"
)

// ScreenshotClient is an interface for capturing screenshots.
// This avoids a circular dependency between utils and control.
type ScreenshotClient interface {
	ScreenshotScaled(scale float64) (image.Image, error)
}

// ScreenState represents the detected UI state
type ScreenState int

const (
	ScreenStateUnknown          ScreenState = iota
	ScreenStateBlack                        // Black screen (booting or off)
	ScreenStateAppleLogo                    // Apple logo during boot
	ScreenStateSetupAssistant               // Setup Assistant (first-run experience)
	ScreenStateLoginScreen                  // Login screen
	ScreenStateDesktop                      // Desktop with dock
	ScreenStateRecoveryMode                 // Recovery mode (generic)
	ScreenStateRecoveryLanguage             // Recovery: language selection
	ScreenStateRecoveryOptions              // Recovery: main options menu
	ScreenStateRecoveryTerminal             // Recovery: Terminal app open
)

// The button in Setup Assistant is typically in the lower third, centered
// It's a small, pill-shaped button (about 100-200px wide)
// Check around 75% down

// Sample the button area (should be lighter - white/gray button)

// Sample the button region

// Sample surrounding area (left and right of button)

// Left side

// Right side

// Setup Assistant button is typically lighter than surroundings (white/light gray)
// A difference of 30+ suggests a visible button

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
