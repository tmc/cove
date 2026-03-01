// setup_assistant.go - Automate macOS Setup Assistant via keyboard navigation
package main

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"time"
)

// SetupAssistant automates the macOS first-run experience
type SetupAssistant struct {
	client    *ControlClient
	config    ProvisionConfig
	verbose   bool
	saveDir   string // Directory to save screenshots for debugging
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

// NewSetupAssistant creates a new setup assistant
func NewSetupAssistant(opts SetupAssistantOptions) *SetupAssistant {
	fullname := opts.Fullname
	if fullname == "" {
		fullname = opts.Username
	}

	client := NewControlClient(opts.SocketPath)
	client.SetTimeout(60 * time.Second) // Screenshots can take a while

	return &SetupAssistant{
		client: client,
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

// Run navigates through Setup Assistant and creates the user
func (s *SetupAssistant) Run() error {
	s.log("Starting Setup Assistant automation")
	s.log("Target user: %s", s.config.Username)

	// Wait for control socket to be available
	s.log("Waiting for control socket...")
	if err := s.client.WaitForConnection(60 * time.Second); err != nil {
		return fmt.Errorf("control socket not available: %w", err)
	}
	s.log("Control socket connected")

	// Wait for screen to stabilize (VM is booting)
	s.log("Waiting for screen to stabilize...")
	if err := s.waitForStableScreen(30 * time.Second); err != nil {
		s.log("Warning: screen stability check failed: %v", err)
	}

	// Detect current screen state
	state, err := s.detectCurrentScreen()
	if err != nil {
		s.log("Warning: initial screen detection failed: %v", err)
	}
	s.log("Initial screen state: %s", state)

	// Navigate through Setup Assistant based on current state
	switch state {
	case ScreenStateBlack, ScreenStateAppleLogo:
		// Still booting, wait for Setup Assistant
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
		// Try Setup Assistant navigation anyway
		return s.navigateSetupAssistant()
	}
}

// navigateSetupAssistant walks through the Setup Assistant screens
func (s *SetupAssistant) navigateSetupAssistant() error {
	s.log("Navigating Setup Assistant...")

	// The Setup Assistant flow varies by macOS version, but generally:
	// 1. Language/Region selection
	// 2. Accessibility options
	// 3. Network (may be automatic for VMs)
	// 4. Migration Assistant (skip)
	// 5. Apple ID (skip)
	// 6. Terms & Conditions (agree)
	// 7. Create Computer Account
	// 8. Express Setup (skip)
	// 9. Analytics (skip)
	// 10. Screen Time (skip)
	// 11. Siri (skip)
	// 12. Choose Look (skip)
	// ... and finally Desktop

	var lastPage string
	stuckCount := 0
	const maxStuckCount = 3

	for step := 0; step < 50; step++ { // Max 50 steps to prevent infinite loop
		s.saveDebugScreenshot(fmt.Sprintf("step_%02d", step))

		state, err := s.detectCurrentScreen()
		if err != nil {
			s.log("Warning: screen detection failed: %v", err)
		}
		s.log("Step %d: screen=%s", step, state)

		if state == ScreenStateDesktop {
			s.log("Reached desktop!")
			return nil
		}

		if state == ScreenStateLoginScreen {
			s.log("Reached login screen - attempting login")
			return s.loginWithCredentials()
		}

		// Detect Setup Assistant page type
		img, screenshotErr := s.client.Screenshot()
		if screenshotErr != nil {
			s.log("Warning: screenshot failed: %v", screenshotErr)
		}
		if img == nil {
			s.log("Warning: no screenshot available")
			time.Sleep(time.Second)
			continue
		}

		page := DetectSetupAssistantPage(img)
		s.log("Detected page: %s", page)

		// Check if we're stuck on the same page
		if page == lastPage && page != "unknown" {
			stuckCount++
			s.log("Stuck count: %d/%d", stuckCount, maxStuckCount)
		} else {
			stuckCount = 0
		}
		lastPage = page

		// Recovery mechanism: if stuck, try escape sequences
		if stuckCount >= maxStuckCount {
			s.log("Stuck on page %s, attempting recovery...", page)
			if s.attemptRecovery(step) {
				stuckCount = 0
				continue
			}
		}

		// Handle specific Setup Assistant pages
		handled := s.handleSetupAssistantPage(page)
		if handled {
			time.Sleep(s.stepDelay)
			continue
		}

		// Generic navigation for unknown pages
		s.log("Using generic navigation for page: %s", page)
		s.genericNavigate()
		time.Sleep(s.stepDelay)
	}

	return fmt.Errorf("setup assistant navigation did not complete within expected steps")
}

// handleSetupAssistantPage handles a specific Setup Assistant page
// Returns true if the page was handled, false for generic navigation
func (s *SetupAssistant) handleSetupAssistantPage(page string) bool {
	switch page {
	case "hello":
		// Press Return to dismiss hello animation
		s.log("Handling hello screen - pressing Return")
		s.pressKey(KeyCodeReturn)
		time.Sleep(2 * time.Second)
		return true

	case "language":
		// Language selection - press Return to accept default (English)
		s.log("Handling language screen - accepting default")
		s.pressKey(KeyCodeReturn)
		time.Sleep(time.Second)
		return true

	case "country_region":
		// Country is usually pre-selected, press Return to continue
		s.log("Handling country/region screen - pressing Return")
		s.pressKey(KeyCodeReturn)
		time.Sleep(time.Second)
		return true

	case "accessibility":
		// Skip accessibility options with Escape or "Not Now"
		s.log("Handling accessibility screen - pressing Escape to skip")
		s.pressKey(KeyCodeEscape)
		time.Sleep(500 * time.Millisecond)
		s.pressKey(KeyCodeReturn) // Confirm skip
		time.Sleep(time.Second)
		return true

	case "wifi", "network":
		// VM uses NAT, skip network setup
		s.log("Handling wifi/network screen - pressing Escape to skip")
		s.pressKey(KeyCodeEscape)
		time.Sleep(500 * time.Millisecond)
		// Some versions need extra confirmation
		s.pressKey(KeyCodeReturn)
		time.Sleep(time.Second)
		return true

	case "migration":
		// Skip Migration Assistant
		s.log("Handling migration screen - selecting 'Not Now'")
		// Tab to "Not Now" button and press Return
		for i := 0; i < 3; i++ {
			s.pressKey(KeyCodeTab)
			time.Sleep(100 * time.Millisecond)
		}
		s.pressKey(KeyCodeReturn)
		time.Sleep(time.Second)
		return true

	case "apple_id", "signin":
		// Skip Apple ID sign in
		s.log("Handling Apple ID screen - selecting 'Set Up Later'")
		// Tab to "Set Up Later" and press Return
		for i := 0; i < 4; i++ {
			s.pressKey(KeyCodeTab)
			time.Sleep(100 * time.Millisecond)
		}
		s.pressKey(KeyCodeReturn)
		time.Sleep(500 * time.Millisecond)
		// Confirm "Skip" in dialog
		s.pressKey(KeyCodeReturn)
		time.Sleep(time.Second)
		return true

	case "terms", "terms_conditions":
		// Agree to Terms & Conditions
		s.log("Handling terms screen - pressing Tab to Agree, then Return")
		// Tab to Agree button
		for i := 0; i < 2; i++ {
			s.pressKey(KeyCodeTab)
			time.Sleep(100 * time.Millisecond)
		}
		s.pressKey(KeyCodeReturn)
		time.Sleep(500 * time.Millisecond)
		// Confirm agreement in dialog
		s.pressKey(KeyCodeReturn)
		time.Sleep(2 * time.Second)
		return true

	case "user_account":
		// Create user account
		s.log("Handling user account screen")
		if err := s.fillUserAccountForm(); err != nil {
			s.log("Warning: user account form failed: %v", err)
		}
		time.Sleep(2 * time.Second)
		return true

	case "express_setup":
		// Customize settings instead of express setup
		s.log("Handling express setup screen - selecting 'Customize Settings'")
		s.pressKey(KeyCodeTab)
		time.Sleep(100 * time.Millisecond)
		s.pressKey(KeyCodeReturn)
		time.Sleep(time.Second)
		return true

	case "analytics":
		// Skip analytics
		s.log("Handling analytics screen - unchecking and continuing")
		// Uncheck checkboxes by pressing Space, then Tab+Return
		s.pressKey(KeyCodeSpace) // Uncheck first checkbox
		time.Sleep(100 * time.Millisecond)
		s.pressKey(KeyCodeTab)
		time.Sleep(100 * time.Millisecond)
		s.pressKey(KeyCodeSpace) // Uncheck second checkbox if present
		time.Sleep(100 * time.Millisecond)
		// Tab to Continue
		for i := 0; i < 3; i++ {
			s.pressKey(KeyCodeTab)
			time.Sleep(100 * time.Millisecond)
		}
		s.pressKey(KeyCodeReturn)
		time.Sleep(time.Second)
		return true

	case "screen_time":
		// Skip Screen Time setup
		s.log("Handling screen time screen - selecting 'Set Up Later'")
		s.pressKey(KeyCodeTab)
		time.Sleep(100 * time.Millisecond)
		s.pressKey(KeyCodeReturn)
		time.Sleep(time.Second)
		return true

	case "siri":
		// Don't enable Siri
		s.log("Handling Siri screen - selecting 'Don't Enable Siri'")
		// Usually need to Tab to the decline option
		s.pressKey(KeyCodeTab)
		time.Sleep(100 * time.Millisecond)
		s.pressKey(KeyCodeReturn)
		time.Sleep(time.Second)
		return true

	case "touch_id":
		// Skip Touch ID setup
		s.log("Handling Touch ID screen - selecting 'Set Up Later'")
		s.pressKey(KeyCodeTab)
		time.Sleep(100 * time.Millisecond)
		s.pressKey(KeyCodeReturn)
		time.Sleep(time.Second)
		return true

	case "choose_look", "appearance":
		// Accept default appearance
		s.log("Handling appearance screen - accepting default")
		s.pressKey(KeyCodeReturn)
		time.Sleep(time.Second)
		return true

	case "privacy":
		// Skip privacy settings
		s.log("Handling privacy screen - continuing")
		s.pressKey(KeyCodeReturn)
		time.Sleep(time.Second)
		return true

	case "filevault":
		// Skip FileVault for VMs
		s.log("Handling FileVault screen - skipping")
		s.pressKey(KeyCodeTab)
		time.Sleep(100 * time.Millisecond)
		s.pressKey(KeyCodeReturn)
		time.Sleep(time.Second)
		return true

	case "icloud_keychain":
		// Skip iCloud Keychain
		s.log("Handling iCloud Keychain screen - skipping")
		s.pressKey(KeyCodeTab)
		time.Sleep(100 * time.Millisecond)
		s.pressKey(KeyCodeReturn)
		time.Sleep(time.Second)
		return true
	}

	return false
}

// genericNavigate attempts generic navigation for unknown pages
func (s *SetupAssistant) genericNavigate() {
	// Try pressing Return first
	s.pressKey(KeyCodeReturn)
	time.Sleep(300 * time.Millisecond)

	// Take another screenshot to check if we advanced
	newImg, err := s.client.Screenshot()
	if err != nil {
		s.log("Warning: screenshot failed during navigation: %v", err)
	}
	if newImg != nil {
		newPage := DetectSetupAssistantPage(newImg)
		if newPage != "unknown" {
			return // We advanced
		}
	}

	// If Return didn't work, try Tab + Return
	s.log("Return didn't advance, trying Tab + Return")
	s.pressKey(KeyCodeTab)
	time.Sleep(100 * time.Millisecond)
	s.pressKey(KeyCodeReturn)
}

// attemptRecovery tries various escape sequences when stuck
func (s *SetupAssistant) attemptRecovery(step int) bool {
	s.log("Attempting recovery sequence...")
	s.saveDebugScreenshot(fmt.Sprintf("recovery_%02d", step))

	// Try 1: Press Escape to dismiss dialogs
	s.log("Recovery: trying Escape")
	s.pressKey(KeyCodeEscape)
	time.Sleep(500 * time.Millisecond)

	img, err := s.client.Screenshot()
	if err != nil {
		s.log("Warning: screenshot failed during recovery: %v", err)
	}
	if img != nil {
		state := DetectScreenState(img)
		if state == ScreenStateDesktop || state == ScreenStateLoginScreen {
			return true
		}
	}

	// Try 2: Cmd+. (Cancel)
	s.log("Recovery: trying Cmd+.")
	s.client.KeyPressWithModifiers(KeyCodePeriod, ModifierCommand)
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

// fillUserAccountForm fills in the user account creation form
func (s *SetupAssistant) fillUserAccountForm() error {
	s.log("Filling user account form...")

	// User account form typically has:
	// - Full Name field
	// - Account Name field
	// - Password field
	// - Verify Password field
	// - Password Hint field (optional)

	// Navigate to first field (Tab to ensure we're in the form)
	for i := 0; i < 3; i++ {
		s.pressKey(KeyCodeTab)
		time.Sleep(100 * time.Millisecond)
	}

	// Full Name
	s.log("Entering full name: %s", s.config.Fullname)
	if err := s.client.TypeText(s.config.Fullname); err != nil {
		return fmt.Errorf("type fullname: %w", err)
	}
	time.Sleep(200 * time.Millisecond)

	// Tab to Account Name (may auto-populate from full name)
	s.pressKey(KeyCodeTab)
	time.Sleep(200 * time.Millisecond)

	// Clear and set username
	s.selectAll()
	time.Sleep(100 * time.Millisecond)
	s.log("Entering username: %s", s.config.Username)
	if err := s.client.TypeText(s.config.Username); err != nil {
		return fmt.Errorf("type username: %w", err)
	}
	time.Sleep(200 * time.Millisecond)

	// Tab to Password
	s.pressKey(KeyCodeTab)
	time.Sleep(200 * time.Millisecond)
	s.log("Entering password")
	if err := s.client.TypeText(s.config.Password); err != nil {
		return fmt.Errorf("type password: %w", err)
	}
	time.Sleep(200 * time.Millisecond)

	// Tab to Verify Password
	s.pressKey(KeyCodeTab)
	time.Sleep(200 * time.Millisecond)
	if err := s.client.TypeText(s.config.Password); err != nil {
		return fmt.Errorf("type verify password: %w", err)
	}
	time.Sleep(200 * time.Millisecond)

	// Tab to Password Hint (skip it)
	s.pressKey(KeyCodeTab)
	time.Sleep(200 * time.Millisecond)

	// Tab to Continue button and press Enter
	s.pressKey(KeyCodeTab)
	time.Sleep(200 * time.Millisecond)
	s.pressKey(KeyCodeReturn)
	time.Sleep(2 * time.Second)

	s.log("User account form submitted")
	return nil
}

// loginWithCredentials attempts to log in at the login screen
func (s *SetupAssistant) loginWithCredentials() error {
	s.log("Attempting login with created credentials")

	// At login screen, click on user icon or type username
	// Then type password and press Enter

	// Usually the user is already selected, just type password
	time.Sleep(500 * time.Millisecond)

	// Type password
	if err := s.client.TypeText(s.config.Password); err != nil {
		return fmt.Errorf("type login password: %w", err)
	}
	time.Sleep(200 * time.Millisecond)

	// Press Enter to login
	s.pressKey(KeyCodeReturn)
	time.Sleep(5 * time.Second)

	// Check if we reached desktop
	state, _ := s.detectCurrentScreen()
	if state == ScreenStateDesktop {
		s.log("Login successful!")
		return nil
	}

	s.log("Login may have failed, current state: %s", state)
	return nil
}

// waitForSetupAssistant waits for Setup Assistant to appear
func (s *SetupAssistant) waitForSetupAssistant(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state, err := s.detectCurrentScreen()
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}

		if state == ScreenStateSetupAssistant {
			return nil
		}
		if state == ScreenStateLoginScreen || state == ScreenStateDesktop {
			return nil // Already past Setup Assistant
		}

		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timeout waiting for Setup Assistant")
}

// waitForStableScreen waits for the screen to stop changing
func (s *SetupAssistant) waitForStableScreen(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastImg image.Image
	stableCount := 0

	for time.Now().Before(deadline) {
		img, err := s.client.ScreenshotScaled(0.25)
		if err != nil {
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

// detectCurrentScreen takes a screenshot and detects the screen state
func (s *SetupAssistant) detectCurrentScreen() (ScreenState, error) {
	img, err := s.client.ScreenshotScaled(0.5)
	if err != nil {
		return ScreenStateUnknown, err
	}
	return DetectScreenState(img), nil
}

// pressKey presses and releases a key
func (s *SetupAssistant) pressKey(keyCode uint16) {
	if err := s.client.KeyPress(keyCode); err != nil {
		s.log("Warning: key press failed: %v", err)
	}
}

// selectAll sends Cmd+A to select all text
func (s *SetupAssistant) selectAll() {
	if err := s.client.KeyPressWithModifiers(KeyCodeA, ModifierCommand); err != nil {
		s.log("Warning: select all failed: %v", err)
	}
}

// saveDebugScreenshot saves a screenshot for debugging
func (s *SetupAssistant) saveDebugScreenshot(name string) {
	if s.saveDir == "" {
		return
	}

	if err := os.MkdirAll(s.saveDir, 0755); err != nil {
		s.log("Warning: failed to create debug dir: %v", err)
		return
	}

	img, err := s.client.Screenshot()
	if err != nil {
		s.log("Warning: failed to capture screenshot: %v", err)
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

// log prints a message if verbose mode is enabled
func (s *SetupAssistant) log(format string, args ...interface{}) {
	if s.verbose {
		fmt.Printf("[SetupAssistant] "+format+"\n", args...)
	}
}

// Wait for desktop

// Open Terminal via Spotlight

// Type the command to enable auto-login

// Enter password for sudo

// Close Terminal

// VerifyProvisioning checks if the user was created successfully
func (s *SetupAssistant) VerifyProvisioning() (bool, error) {
	state, err := s.detectCurrentScreen()
	if err != nil {
		return false, err
	}

	if state == ScreenStateDesktop {
		s.log("Verification: At desktop - provisioning appears successful")
		return true, nil
	}

	if state == ScreenStateLoginScreen {
		s.log("Verification: At login screen - user created, needs login")
		return true, nil
	}

	s.log("Verification: Unexpected state: %s", state)
	return false, nil
}
