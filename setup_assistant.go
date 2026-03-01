// setup_assistant.go - Automate macOS Setup Assistant via OCR-driven navigation
package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// SetupAssistant automates the macOS first-run experience using OCR-driven
// screen detection and click targeting. Falls back to keyboard navigation
// when OCR is unavailable.
type SetupAssistant struct {
	cs        *ControlServer // direct server reference for in-process use
	client    *ControlClient // socket client for out-of-process use (ctl command)
	ocr       *OCRService
	config    ProvisionConfig
	verbose   bool
	saveDir   string // directory to save screenshots for debugging
	stepDelay time.Duration
}

// SetupAssistantOptions configures the setup assistant
type SetupAssistantOptions struct {
	SocketPath string
	Username   string
	Password   string
	Fullname   string
	Admin      bool
	Verbose    bool
	SaveDir    string // Optional: save screenshots to this directory
}

// NewSetupAssistant creates a setup assistant that communicates via socket.
func NewSetupAssistant(opts SetupAssistantOptions) *SetupAssistant {
	fullname := opts.Fullname
	if fullname == "" {
		fullname = opts.Username
	}

	client := NewControlClient(opts.SocketPath)
	client.SetTimeout(60 * time.Second)

	return &SetupAssistant{
		client: client,
		ocr:    NewOCRService(opts.Verbose),
		config: ProvisionConfig{
			Username: opts.Username,
			Password: opts.Password,
			Fullname: fullname,
			Admin:    opts.Admin,
		},
		verbose:   opts.Verbose,
		saveDir:   opts.SaveDir,
		stepDelay: 500 * time.Millisecond,
	}
}

// NewSetupAssistantInProcess creates a setup assistant that uses the
// ControlServer directly, avoiding socket overhead for in-process automation.
func NewSetupAssistantInProcess(cs *ControlServer, ocr *OCRService, config ProvisionConfig, verbose bool, saveDir string) *SetupAssistant {
	if config.Fullname == "" {
		config.Fullname = config.Username
	}
	return &SetupAssistant{
		cs:        cs,
		ocr:       ocr,
		config:    config,
		verbose:   verbose,
		saveDir:   saveDir,
		stepDelay: 500 * time.Millisecond,
	}
}

// Run navigates through Setup Assistant and creates the user.
func (s *SetupAssistant) Run() error {
	s.log("Starting Setup Assistant automation")
	s.log("Target user: %s", s.config.Username)

	// Wait for control socket to be available (only for socket-based mode)
	if s.client != nil {
		s.log("Waiting for control socket...")
		if err := s.client.WaitForConnection(60 * time.Second); err != nil {
			return fmt.Errorf("control socket not available: %w", err)
		}
		s.log("Control socket connected")
	}

	// Wait for screen to stabilize (VM is booting)
	s.log("Waiting for screen to stabilize...")
	if err := s.waitForStableScreen(30 * time.Second); err != nil {
		s.log("Warning: screen stability check failed: %v", err)
	}

	// Detect current screen state using OCR if available
	state := s.detectCurrentScreenOCR()
	s.log("Initial screen state: %s", state)

	switch state {
	case ScreenStateBlack, ScreenStateAppleLogo:
		s.log("Waiting for Setup Assistant to appear...")
		if err := s.waitForSetupAssistant(180 * time.Second); err != nil {
			return err
		}
		return s.navigateSetupAssistant()

	case ScreenStateSetupAssistant:
		return s.navigateSetupAssistant()

	case ScreenStateLoginScreen:
		s.log("Already at login screen - Setup Assistant may have been completed")
		return nil

	case ScreenStateDesktop:
		s.log("Already at desktop - Setup Assistant completed")
		return nil

	default:
		return s.navigateSetupAssistant()
	}
}

// navigateSetupAssistant walks through Setup Assistant screens using OCR.
func (s *SetupAssistant) navigateSetupAssistant() error {
	s.log("Navigating Setup Assistant (OCR-driven)...")

	var lastPage string
	stuckCount := 0
	const maxStuckCount = 3

	for step := 0; step < 50; step++ {
		s.saveDebugScreenshot(fmt.Sprintf("step_%02d", step))

		// Detect page using OCR
		page := s.detectPage()
		s.log("Step %d: page=%s", step, page)

		if page == "desktop" {
			s.log("Reached desktop!")
			return nil
		}
		if page == "login" {
			s.log("Reached login screen - attempting login")
			return s.loginWithCredentials()
		}

		// Check if we're stuck
		if page == lastPage && page != "unknown" {
			stuckCount++
			s.log("Stuck count: %d/%d", stuckCount, maxStuckCount)
		} else {
			stuckCount = 0
		}
		lastPage = page

		if stuckCount >= maxStuckCount {
			s.log("Stuck on page %s, attempting recovery...", page)
			if s.attemptRecovery(step) {
				stuckCount = 0
				continue
			}
		}

		// Handle the page with OCR-driven clicks
		handled := s.handlePage(page)
		if handled {
			// Verify we advanced to a new page
			time.Sleep(s.stepDelay)
			s.waitForPageChange(page, 5*time.Second)
			continue
		}

		// Generic navigation fallback
		s.log("Using generic navigation for page: %s", page)
		s.genericNavigate()
		time.Sleep(s.stepDelay)
	}

	return fmt.Errorf("setup assistant navigation did not complete within expected steps")
}

// handlePage handles a specific Setup Assistant page using OCR-driven clicks.
// Returns true if the page was handled.
func (s *SetupAssistant) handlePage(page string) bool {
	switch page {
	case "hello":
		s.log("Handling hello screen")
		// Hello screen responds to Return or clicking Continue
		if s.tryOCRClick("Continue", 3*time.Second) == nil {
			return true
		}
		s.pressKey(KeyCodeReturn)
		time.Sleep(2 * time.Second)
		return true

	case "language":
		s.log("Handling language screen - selecting English")
		if s.tryOCRClick("English", 3*time.Second) == nil {
			time.Sleep(300 * time.Millisecond)
			s.tryOCRClick("Continue", 3*time.Second)
			return true
		}
		s.pressKey(KeyCodeReturn)
		time.Sleep(time.Second)
		return true

	case "country_region":
		s.log("Handling country/region screen")
		if s.tryOCRClick("Continue", 3*time.Second) == nil {
			return true
		}
		s.pressKey(KeyCodeReturn)
		time.Sleep(time.Second)
		return true

	case "accessibility":
		s.log("Handling accessibility screen")
		if s.tryOCRClick("Not Now", 3*time.Second) == nil {
			return true
		}
		s.pressKey(KeyCodeEscape)
		time.Sleep(500 * time.Millisecond)
		s.pressKey(KeyCodeReturn)
		time.Sleep(time.Second)
		return true

	case "wifi", "network":
		s.log("Handling wifi/network screen - skipping")
		if s.tryOCRClick("Other Network Options", 2*time.Second) == nil {
			time.Sleep(500 * time.Millisecond)
			s.tryOCRClick("My computer does not connect to the internet", 2*time.Second)
			time.Sleep(500 * time.Millisecond)
			s.tryOCRClick("Continue", 2*time.Second)
			return true
		}
		s.pressKey(KeyCodeEscape)
		time.Sleep(500 * time.Millisecond)
		s.pressKey(KeyCodeReturn)
		time.Sleep(time.Second)
		return true

	case "migration":
		s.log("Handling migration screen - selecting 'Not Now'")
		if s.tryOCRClick("Not Now", 3*time.Second) == nil {
			return true
		}
		// Fallback: tab to Not Now
		for i := 0; i < 3; i++ {
			s.pressKey(KeyCodeTab)
			time.Sleep(100 * time.Millisecond)
		}
		s.pressKey(KeyCodeReturn)
		time.Sleep(time.Second)
		return true

	case "apple_id", "signin":
		s.log("Handling Apple ID screen - selecting 'Set Up Later'")
		if s.tryOCRClick("Set Up Later", 3*time.Second) == nil {
			time.Sleep(time.Second)
			// Confirm skip dialog
			s.tryOCRClick("Skip", 3*time.Second)
			return true
		}
		// Fallback
		for i := 0; i < 4; i++ {
			s.pressKey(KeyCodeTab)
			time.Sleep(100 * time.Millisecond)
		}
		s.pressKey(KeyCodeReturn)
		time.Sleep(500 * time.Millisecond)
		s.pressKey(KeyCodeReturn)
		time.Sleep(time.Second)
		return true

	case "terms":
		s.log("Handling terms screen - clicking Agree")
		if s.tryOCRClick("Agree", 3*time.Second) == nil {
			time.Sleep(time.Second)
			// Confirm agreement dialog
			s.tryOCRClick("Agree", 3*time.Second)
			return true
		}
		// Fallback
		for i := 0; i < 2; i++ {
			s.pressKey(KeyCodeTab)
			time.Sleep(100 * time.Millisecond)
		}
		s.pressKey(KeyCodeReturn)
		time.Sleep(500 * time.Millisecond)
		s.pressKey(KeyCodeReturn)
		time.Sleep(2 * time.Second)
		return true

	case "user_account":
		s.log("Handling user account screen")
		if err := s.fillUserAccountForm(); err != nil {
			s.log("Warning: user account form failed: %v", err)
		}
		time.Sleep(2 * time.Second)
		return true

	case "express_setup":
		s.log("Handling express setup screen")
		if s.tryOCRClick("Customize Settings", 3*time.Second) == nil {
			return true
		}
		s.pressKey(KeyCodeTab)
		time.Sleep(100 * time.Millisecond)
		s.pressKey(KeyCodeReturn)
		time.Sleep(time.Second)
		return true

	case "analytics":
		s.log("Handling analytics screen")
		if s.tryOCRClick("Continue", 3*time.Second) == nil {
			return true
		}
		// Fallback: skip through checkboxes then continue
		s.pressKey(KeyCodeSpace)
		time.Sleep(100 * time.Millisecond)
		s.pressKey(KeyCodeTab)
		time.Sleep(100 * time.Millisecond)
		s.pressKey(KeyCodeSpace)
		time.Sleep(100 * time.Millisecond)
		for i := 0; i < 3; i++ {
			s.pressKey(KeyCodeTab)
			time.Sleep(100 * time.Millisecond)
		}
		s.pressKey(KeyCodeReturn)
		time.Sleep(time.Second)
		return true

	case "screen_time":
		s.log("Handling screen time screen")
		if s.tryOCRClick("Set Up Later", 3*time.Second) == nil {
			return true
		}
		if s.tryOCRClick("Continue", 3*time.Second) == nil {
			return true
		}
		s.pressKey(KeyCodeTab)
		time.Sleep(100 * time.Millisecond)
		s.pressKey(KeyCodeReturn)
		time.Sleep(time.Second)
		return true

	case "siri":
		s.log("Handling Siri screen")
		if s.tryOCRClick("Continue", 3*time.Second) == nil {
			return true
		}
		s.pressKey(KeyCodeTab)
		time.Sleep(100 * time.Millisecond)
		s.pressKey(KeyCodeReturn)
		time.Sleep(time.Second)
		return true

	case "touch_id":
		s.log("Handling Touch ID screen")
		if s.tryOCRClick("Set Up Later", 3*time.Second) == nil {
			return true
		}
		s.pressKey(KeyCodeTab)
		time.Sleep(100 * time.Millisecond)
		s.pressKey(KeyCodeReturn)
		time.Sleep(time.Second)
		return true

	case "appearance", "choose_look":
		s.log("Handling appearance screen")
		if s.tryOCRClick("Continue", 3*time.Second) == nil {
			return true
		}
		s.pressKey(KeyCodeReturn)
		time.Sleep(time.Second)
		return true

	case "privacy":
		s.log("Handling privacy screen")
		if s.tryOCRClick("Continue", 3*time.Second) == nil {
			return true
		}
		s.pressKey(KeyCodeReturn)
		time.Sleep(time.Second)
		return true

	case "filevault":
		s.log("Handling FileVault screen - skipping")
		if s.tryOCRClick("Continue", 3*time.Second) == nil {
			return true
		}
		s.pressKey(KeyCodeTab)
		time.Sleep(100 * time.Millisecond)
		s.pressKey(KeyCodeReturn)
		time.Sleep(time.Second)
		return true

	case "icloud_keychain":
		s.log("Handling iCloud Keychain screen - skipping")
		if s.tryOCRClick("Set Up Later", 3*time.Second) == nil {
			return true
		}
		s.pressKey(KeyCodeTab)
		time.Sleep(100 * time.Millisecond)
		s.pressKey(KeyCodeReturn)
		time.Sleep(time.Second)
		return true
	}

	return false
}

// tryOCRClick attempts to find and click text using OCR.
// Returns nil on success, error on failure (text not found or click failed).
func (s *SetupAssistant) tryOCRClick(text string, timeout time.Duration) error {
	if s.cs != nil && s.ocr != nil {
		return s.cs.OCRClickText(s.ocr, text, timeout)
	}
	// Socket-based mode: take screenshot, find text, click
	if s.client != nil && s.ocr != nil {
		return s.ocrClickViaClient(text, timeout)
	}
	return fmt.Errorf("no OCR or control path available")
}

// ocrClickViaClient uses the socket client for screenshot + OCR + click.
func (s *SetupAssistant) ocrClickViaClient(text string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		img, err := s.client.Screenshot()
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		normX, normY, found := s.ocr.FindTextNormalized(img, text)
		if found {
			s.log("OCR found %q at (%.3f, %.3f) — clicking", text, normX, normY)
			return s.client.MouseClick(normX, normY)
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout: text %q not found", text)
}

// detectPage uses OCR to identify the current Setup Assistant page.
func (s *SetupAssistant) detectPage() string {
	if s.cs != nil && s.ocr != nil {
		return s.cs.OCRDetectPage(s.ocr)
	}
	if s.client != nil && s.ocr != nil {
		img, err := s.client.Screenshot()
		if err != nil {
			return "unknown"
		}
		return OCRDetectSetupAssistantPage(img, s.ocr)
	}
	// Fallback to pixel-based detection
	if s.client != nil {
		img, err := s.client.ScreenshotScaled(0.5)
		if err != nil {
			return "unknown"
		}
		return DetectSetupAssistantPage(img)
	}
	return "unknown"
}

// detectCurrentScreenOCR detects screen state using OCR when available.
func (s *SetupAssistant) detectCurrentScreenOCR() ScreenState {
	if s.cs != nil {
		img, errMsg := s.cs.captureVMView()
		if errMsg != "" {
			return ScreenStateUnknown
		}
		return DetectScreenStateOCR(img, s.ocr)
	}
	if s.client != nil {
		img, err := s.client.ScreenshotScaled(0.5)
		if err != nil {
			return ScreenStateUnknown
		}
		if s.ocr != nil {
			return DetectScreenStateOCR(img, s.ocr)
		}
		return DetectScreenState(img)
	}
	return ScreenStateUnknown
}

// waitForPageChange polls until the detected page differs from currentPage.
func (s *SetupAssistant) waitForPageChange(currentPage string, timeout time.Duration) {
	if s.cs != nil && s.ocr != nil {
		s.cs.OCRWaitForPageChange(s.ocr, currentPage, timeout)
		return
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		newPage := s.detectPage()
		if newPage != currentPage {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// genericNavigate attempts generic navigation for unknown pages.
func (s *SetupAssistant) genericNavigate() {
	// Try clicking "Continue" via OCR first
	if s.tryOCRClick("Continue", 2*time.Second) == nil {
		return
	}

	// Fall back to keyboard
	s.pressKey(KeyCodeReturn)
	time.Sleep(300 * time.Millisecond)

	// Check if we advanced
	newPage := s.detectPage()
	if newPage != "unknown" {
		return
	}

	s.log("Return didn't advance, trying Tab + Return")
	s.pressKey(KeyCodeTab)
	time.Sleep(100 * time.Millisecond)
	s.pressKey(KeyCodeReturn)
}

// attemptRecovery tries various escape sequences when stuck.
func (s *SetupAssistant) attemptRecovery(step int) bool {
	s.log("Attempting recovery sequence...")
	s.saveDebugScreenshot(fmt.Sprintf("recovery_%02d", step))

	// Try 1: Press Escape to dismiss dialogs
	s.log("Recovery: trying Escape")
	s.pressKey(KeyCodeEscape)
	time.Sleep(500 * time.Millisecond)

	page := s.detectPage()
	if page == "desktop" || page == "login" {
		return true
	}

	// Try 2: Cmd+. (Cancel)
	s.log("Recovery: trying Cmd+.")
	if s.client != nil {
		s.client.KeyPressWithModifiers(KeyCodePeriod, ModifierCommand)
	} else if s.cs != nil {
		s.cs.sendKeyEvent(&controlpb.KeyCommand{
			KeyCode:   uint32(KeyCodePeriod),
			KeyDown:   true,
			Modifiers: uint32(ModifierCommand),
		})
		s.cs.sendKeyEvent(&controlpb.KeyCommand{
			KeyCode:   uint32(KeyCodePeriod),
			KeyDown:   false,
			Modifiers: uint32(ModifierCommand),
		})
	}
	time.Sleep(500 * time.Millisecond)

	// Try 3: Tab cycle through buttons then Return
	s.log("Recovery: trying Tab cycle")
	for i := 0; i < 5; i++ {
		s.pressKey(KeyCodeTab)
		time.Sleep(100 * time.Millisecond)
	}
	s.pressKey(KeyCodeReturn)
	time.Sleep(time.Second)

	return false
}

// fillUserAccountForm fills in the user account creation form.
// Uses OCR to click fields when available, falls back to tab navigation.
func (s *SetupAssistant) fillUserAccountForm() error {
	s.log("Filling user account form...")

	// Try OCR-driven field selection first
	if s.ocr != nil {
		if err := s.tryOCRClick("Full Name", 2*time.Second); err == nil {
			time.Sleep(200 * time.Millisecond)
			s.log("Entering full name: %s", s.config.Fullname)
			s.typeText(s.config.Fullname)
			time.Sleep(200 * time.Millisecond)

			// Account Name field
			s.pressKey(KeyCodeTab)
			time.Sleep(200 * time.Millisecond)
			s.selectAll()
			time.Sleep(100 * time.Millisecond)
			s.log("Entering username: %s", s.config.Username)
			s.typeText(s.config.Username)
			time.Sleep(200 * time.Millisecond)

			// Password
			s.pressKey(KeyCodeTab)
			time.Sleep(200 * time.Millisecond)
			s.log("Entering password")
			s.typeText(s.config.Password)
			time.Sleep(200 * time.Millisecond)

			// Verify Password
			s.pressKey(KeyCodeTab)
			time.Sleep(200 * time.Millisecond)
			s.typeText(s.config.Password)
			time.Sleep(200 * time.Millisecond)

			// Skip hint, click Continue
			s.pressKey(KeyCodeTab)
			time.Sleep(200 * time.Millisecond)
			if s.tryOCRClick("Continue", 2*time.Second) != nil {
				s.pressKey(KeyCodeTab)
				time.Sleep(200 * time.Millisecond)
				s.pressKey(KeyCodeReturn)
			}
			time.Sleep(2 * time.Second)

			s.log("User account form submitted")
			return nil
		}
	}

	// Fallback: tab-based navigation
	for i := 0; i < 3; i++ {
		s.pressKey(KeyCodeTab)
		time.Sleep(100 * time.Millisecond)
	}

	s.log("Entering full name: %s", s.config.Fullname)
	s.typeText(s.config.Fullname)
	time.Sleep(200 * time.Millisecond)

	s.pressKey(KeyCodeTab)
	time.Sleep(200 * time.Millisecond)
	s.selectAll()
	time.Sleep(100 * time.Millisecond)
	s.log("Entering username: %s", s.config.Username)
	s.typeText(s.config.Username)
	time.Sleep(200 * time.Millisecond)

	s.pressKey(KeyCodeTab)
	time.Sleep(200 * time.Millisecond)
	s.log("Entering password")
	s.typeText(s.config.Password)
	time.Sleep(200 * time.Millisecond)

	s.pressKey(KeyCodeTab)
	time.Sleep(200 * time.Millisecond)
	s.typeText(s.config.Password)
	time.Sleep(200 * time.Millisecond)

	s.pressKey(KeyCodeTab)
	time.Sleep(200 * time.Millisecond)
	s.pressKey(KeyCodeTab)
	time.Sleep(200 * time.Millisecond)
	s.pressKey(KeyCodeReturn)
	time.Sleep(2 * time.Second)

	s.log("User account form submitted")
	return nil
}

// loginWithCredentials attempts to log in at the login screen.
func (s *SetupAssistant) loginWithCredentials() error {
	s.log("Attempting login with created credentials")
	time.Sleep(500 * time.Millisecond)

	s.typeText(s.config.Password)
	time.Sleep(200 * time.Millisecond)
	s.pressKey(KeyCodeReturn)
	time.Sleep(5 * time.Second)

	page := s.detectPage()
	if page == "desktop" {
		s.log("Login successful!")
		return nil
	}

	s.log("Login may have failed, current page: %s", page)
	return nil
}

// waitForSetupAssistant waits for Setup Assistant to appear.
func (s *SetupAssistant) waitForSetupAssistant(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state := s.detectCurrentScreenOCR()
		if state == ScreenStateSetupAssistant {
			return nil
		}
		if state == ScreenStateLoginScreen || state == ScreenStateDesktop {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timeout waiting for Setup Assistant")
}

// waitForStableScreen waits for the screen to stop changing.
func (s *SetupAssistant) waitForStableScreen(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastImg image.Image
	stableCount := 0

	for time.Now().Before(deadline) {
		img := s.screenshotScaled(0.25)
		if img == nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}

		if lastImg != nil && !IsScreenChanging(lastImg, img, 2.0) {
			stableCount++
			if stableCount >= 3 {
				return nil
			}
		} else {
			stableCount = 0
		}

		lastImg = img
		time.Sleep(500 * time.Millisecond)
	}
	return nil
}

// screenshotScaled captures a scaled screenshot via either path.
func (s *SetupAssistant) screenshotScaled(scale float64) image.Image {
	if s.cs != nil {
		img, errMsg := s.cs.captureVMView()
		if errMsg != "" {
			return nil
		}
		if scale < 1 {
			return scaleImage(img, scale)
		}
		return img
	}
	if s.client != nil {
		img, err := s.client.ScreenshotScaled(scale)
		if err != nil {
			return nil
		}
		return img
	}
	return nil
}

// pressKey presses and releases a key via either path.
func (s *SetupAssistant) pressKey(keyCode uint16) {
	if s.cs != nil {
		s.cs.sendKeyEvent(&controlpb.KeyCommand{KeyCode: uint32(keyCode), KeyDown: true})
		time.Sleep(50 * time.Millisecond)
		s.cs.sendKeyEvent(&controlpb.KeyCommand{KeyCode: uint32(keyCode), KeyDown: false})
		return
	}
	if s.client != nil {
		if err := s.client.KeyPress(keyCode); err != nil {
			s.log("Warning: key press failed: %v", err)
		}
	}
}

// typeText types a string via either path.
func (s *SetupAssistant) typeText(text string) {
	if s.cs != nil {
		s.cs.typeText(&controlpb.TextCommand{Text: text})
		return
	}
	if s.client != nil {
		if err := s.client.TypeText(text); err != nil {
			s.log("Warning: type text failed: %v", err)
		}
	}
}

// selectAll sends Cmd+A to select all text.
func (s *SetupAssistant) selectAll() {
	if s.cs != nil {
		s.cs.sendKeyEvent(&controlpb.KeyCommand{
			KeyCode:   uint32(KeyCodeA),
			KeyDown:   true,
			Modifiers: uint32(ModifierCommand),
		})
		time.Sleep(50 * time.Millisecond)
		s.cs.sendKeyEvent(&controlpb.KeyCommand{
			KeyCode:   uint32(KeyCodeA),
			KeyDown:   false,
			Modifiers: uint32(ModifierCommand),
		})
		return
	}
	if s.client != nil {
		if err := s.client.KeyPressWithModifiers(KeyCodeA, ModifierCommand); err != nil {
			s.log("Warning: select all failed: %v", err)
		}
	}
}

// saveDebugScreenshot saves a screenshot for debugging.
func (s *SetupAssistant) saveDebugScreenshot(name string) {
	if s.saveDir == "" {
		return
	}

	if err := os.MkdirAll(s.saveDir, 0755); err != nil {
		s.log("Warning: failed to create debug dir: %v", err)
		return
	}

	var img image.Image
	if s.cs != nil {
		captured, errMsg := s.cs.captureVMView()
		if errMsg != "" {
			s.log("Warning: failed to capture screenshot: %s", errMsg)
			return
		}
		img = captured
	} else if s.client != nil {
		captured, err := s.client.Screenshot()
		if err != nil {
			s.log("Warning: failed to capture screenshot: %v", err)
			return
		}
		img = captured
	} else {
		return
	}

	path := filepath.Join(s.saveDir, fmt.Sprintf("%s_%d.png", name, time.Now().Unix()))
	f, err := os.Create(path)
	if err != nil {
		s.log("Warning: failed to create screenshot file: %v", err)
		return
	}
	defer f.Close()

	if err := png.Encode(f, img); err != nil {
		s.log("Warning: failed to encode screenshot: %v", err)
		return
	}
	s.log("Saved debug screenshot: %s", path)
}

// log prints a message if verbose mode is enabled.
func (s *SetupAssistant) log(format string, args ...interface{}) {
	if s.verbose {
		fmt.Printf("[SetupAssistant] "+format+"\n", args...)
	}
}

// VerifyProvisioning checks if the user was created successfully.
func (s *SetupAssistant) VerifyProvisioning() (bool, error) {
	page := s.detectPage()

	if page == "desktop" {
		s.log("Verification: At desktop - provisioning appears successful")
		return true, nil
	}

	if page == "login" {
		s.log("Verification: At login screen - user created, needs login")
		return true, nil
	}

	state := s.detectCurrentScreenOCR()
	if state == ScreenStateDesktop {
		s.log("Verification: At desktop - provisioning appears successful")
		return true, nil
	}
	if state == ScreenStateLoginScreen {
		s.log("Verification: At login screen - user created, needs login")
		return true, nil
	}

	s.log("Verification: Unexpected state: %s / page: %s", state, page)
	return false, nil
}
