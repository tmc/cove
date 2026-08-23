package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/tmc/cove/internal/controlserver"
	"github.com/tmc/cove/internal/vmrun"
	"github.com/tmc/cove/proto/controlpb"

	ocrx "github.com/tmc/apple/x/vzkit/ocr"
)

const (
	loginConsoleUserTimeout = 90 * time.Second
	loginScreenRetryDelay   = 8 * time.Second

	// desktopNoConsoleUserConfirmations is how many consecutive conclusive
	// "no console user" answers are required before a desktop classification
	// is downgraded to a login screen.
	desktopNoConsoleUserConfirmations = 3

	// unknownConsoleUserGrace bounds how long the watchdog waits on an
	// unreachable guest agent while the screen looks like a desktop. After
	// it elapses the watchdog assumes the session is logged in and exits.
	unknownConsoleUserGrace = 45 * time.Second

	// loginScreenConfirmations is how many consecutive login-screen
	// classifications are required before typing the cached password when
	// the console user cannot be determined. Screen detection is heuristic
	// ("light overall, top brighter than bottom"), so a light wallpaper or a
	// transient compositing frame during boot can look like a login screen
	// for a frame or two; a real login screen stays put. Three samples at
	// the watchdog's 3s poll interval means roughly ten seconds of stable
	// classification, which no transient survives and which delays a genuine
	// automated login only slightly.
	loginScreenConfirmations = 3

	// loginScreenConfirmInterval is the delay between the extra screen
	// samples tryLoginFallback takes when it has to corroborate a login
	// screen on its own.
	loginScreenConfirmInterval = 2 * time.Second
)

// loginTypeAction is the outcome of the "may we type the cached password now?"
// decision.
type loginTypeAction int

const (
	// loginTypeWait means the evidence is not yet sufficient; sample again.
	loginTypeWait loginTypeAction = iota
	// loginTypeType means it is safe to type the cached password.
	loginTypeType
	// loginTypeAbort means somebody is logged in; stop trying entirely.
	loginTypeAbort
)

// loginTypeDecision decides whether the cached password may be typed, given a
// screen classification, the result of a console-user query, and how many
// consecutive login-screen classifications have been observed.
//
// Screen detection alone is never enough: DetectScreen is a pixel heuristic
// and a real desktop can classify as a login screen. The console user is the
// corroborating evidence.
//
//   - A console user exists: somebody is logged in, abort.
//   - Login screen plus a conclusive controlserver.ErrNoConsoleUser: type.
//   - Login screen with an inconclusive console query (the usual case at a
//     genuine login screen, where the user agent is not up yet): type only
//     once the classification has held for loginScreenConfirmations samples.
//   - Anything else: wait.
func loginTypeDecision(state ScreenState, screenErr error, consoleUser string, consoleErr error, loginStreak int) (loginTypeAction, string) {
	if screenErr != nil {
		return loginTypeWait, fmt.Sprintf("screen state unknown: %v", screenErr)
	}
	if consoleErr == nil && consoleUser != "" {
		return loginTypeAbort, fmt.Sprintf("console user %q is logged in", consoleUser)
	}
	if state != ScreenStateLoginScreen {
		return loginTypeWait, fmt.Sprintf("screen state is %s", state)
	}
	if errors.Is(consoleErr, controlserver.ErrNoConsoleUser) {
		return loginTypeType, ""
	}
	if loginStreak >= loginScreenConfirmations {
		return loginTypeType, ""
	}
	return loginTypeWait, fmt.Sprintf("login screen unconfirmed (%d/%d), console user unknown: %v",
		loginStreak, loginScreenConfirmations, consoleErr)
}

// runProvisioningAutomation starts the Setup Assistant automation using
// the ControlServer directly (in-process) for reliable HID-based input.
//
// It waits for the VM window to be capturable before starting, since the
// ControlServer needs windowNum and viewContentHeight to take screenshots.
func runProvisioningAutomation(cs *ControlServer, rc vmrun.RunConfig) {
	fmt.Println("\n=== Starting Auto-Provisioning (GUI) ===")
	fmt.Printf("Username: %s\n", rc.ProvisionUser)
	fmt.Printf("Admin: %v\n", rc.ProvisionAdmin)
	fmt.Println()
	vmDirectory := cs.effectiveVMDir()

	restoreBackends := forceSetupAutomationBackends(cs)
	defer restoreBackends()

	// Wait for the VM window to be ready for screenshots.
	// The ControlServer is already initialized with SetVMViewWithWindow,
	// but the VM may still be booting and the window not yet rendered.
	if err := waitForVMScreenReady(cs, 120*time.Second); err != nil {
		fmt.Printf("warning: VM screen not ready: %v\n", err)
		fmt.Println("Continuing anyway — automation may recover during page detection.")
	}

	ocr := ocrx.NewService(verbose)
	debugDir := filepath.Join(vmDirectory, "provision_screenshots")
	if debugOCR {
		fmt.Printf("OCR debug screenshots: %s\n", debugDir)
	}

	assistant := NewSetupAssistantInProcess(cs, ocr, ProvisionConfig{
		Username: rc.ProvisionUser,
		Password: rc.ProvisionPassword,
		Fullname: rc.ProvisionUser,
		Admin:    rc.ProvisionAdmin,
	}, verbose, debugDir)

	// Run the automation
	if err := assistant.Run(); err != nil {
		fmt.Printf("Setup Assistant automation failed: %v\n", err)
		fmt.Println("Attempting login screen fallback...")

		socketPath := GetControlSocketPathForVM(vmDirectory)
		creds := loginScreenCredentials{Username: rc.ProvisionUser, Password: rc.ProvisionPassword}
		if loginErr := tryLoginFallback(socketPath, creds, false); loginErr != nil {
			fmt.Printf("Login fallback also failed: %v\n", loginErr)
			fmt.Println("Manual intervention may be required.")
			return
		}
	}

	// Verify provisioning
	success, err := assistant.VerifyProvisioning()
	if err != nil {
		fmt.Printf("Verification error: %v\n", err)
	} else if success {
		fmt.Println("\n=== Provisioning Complete ===")
		fmt.Printf("User '%s' has been created.\n", rc.ProvisionUser)
	} else {
		fmt.Println("\n=== Provisioning Incomplete ===")
		fmt.Println("Please complete setup manually.")
	}
}

// waitForVMScreenReady polls captureDisplayImage until it returns a valid image,
// indicating the VM window is rendered and screenshots are working.
func waitForVMScreenReady(cs *ControlServer, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		img, errMsg := cs.captureDisplayImage()
		if errMsg == "" && img != nil {
			if verbose {
				fmt.Printf("[provision] VM screen ready (%dx%d)\n",
					img.Bounds().Dx(), img.Bounds().Dy())
			}
			return nil
		}
		if verbose {
			fmt.Printf("[provision] waiting for VM screen: %s\n", errMsg)
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timeout waiting for VM screen")
}

// runLoginScreenWatchdog waits for the VM screen to be capturable, then polls
// for a login screen for up to 3 minutes. If one appears, it types the cached
// password. This is the auto-login fallback for headed boots where the guest
// reaches a password prompt instead of the desktop.
//
// The watchdog exits silently if it never sees a login screen — that means
// kcpassword worked and the desktop appeared directly.
func runLoginScreenWatchdog(cs *ControlServer, creds loginScreenCredentials) {
	restoreBackends := forceSetupAutomationBackends(cs)
	defer restoreBackends()

	if err := waitForVMScreenReady(cs, 120*time.Second); err != nil {
		if verbose {
			fmt.Printf("[login-watchdog] VM screen not ready: %v\n", err)
		}
		return
	}

	ocr := ocrx.NewService(verbose)
	assistant := NewSetupAssistantInProcess(cs, ocr, ProvisionConfig{
		Username: creds.Username,
		Password: creds.Password,
	}, verbose, "")
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		switch cs.OCRDetectPage(ocr) {
		case "desktop":
			fmt.Println("Login complete — reached desktop")
			return
		case "login":
			fmt.Println("Login screen detected — typing cached password")
			if err := assistant.loginWithCredentials(); err != nil && verbose {
				fmt.Printf("[login-watchdog] login attempt: %v\n", err)
			}
		}
		time.Sleep(3 * time.Second)
	}
	if verbose {
		fmt.Println("[login-watchdog] timeout — desktop never reached")
	}
}

// tryLoginFallback attempts to login at the login screen using keyboard automation.
func tryLoginFallback(socketPath string, creds loginScreenCredentials, force bool) error {
	if !creds.Valid() {
		return fmt.Errorf("no cached login credentials")
	}

	client := NewControlClient(socketPath)

	if err := client.WaitForConnection(30 * time.Second); err != nil {
		return fmt.Errorf("control socket not available: %w", err)
	}

	// Corroborate before typing, regardless of caller. force only relaxes the
	// screen classification (a desktop the caller downgraded); it never
	// removes the requirement that nobody is logged in.
	if err := confirmLoginTypeAllowed(client, force); err != nil {
		return err
	}
	fmt.Println("Detected login screen - attempting keyboard login...")

	if err := client.MouseClick(0.5, 0.78); err != nil && verbose {
		fmt.Printf("warning: focus password field: %v\n", err)
	}
	time.Sleep(250 * time.Millisecond)

	if err := client.TypeText(creds.Password); err != nil {
		return fmt.Errorf("type password: %w", err)
	}
	time.Sleep(500 * time.Millisecond)

	if err := client.SendKey(36); err != nil {
		return fmt.Errorf("press return: %w", err)
	}

	if err := waitForLoginConsoleUser(client, creds.Username, loginConsoleUserTimeout, true); err == nil {
		fmt.Println("Login successful - reached desktop")
		return nil
	} else if verbose {
		fmt.Printf("[login-watchdog] first login attempt did not reach desktop: %v\n", err)
	}

	// The first attempt may have succeeded even though waitForLoginConsoleUser
	// reported an error: right after a login the guest agent is usually not
	// reachable yet, so every poll returns an inconclusive query error. Never
	// type the password again without fresh, conclusive evidence that the
	// screen is still a login screen.
	if user, err := loginConsoleUser(client); err == nil && user == creds.Username {
		fmt.Println("Login successful - reached desktop")
		return nil
	}
	_, retryState, retryScreenErr := client.DetectScreen()
	_, retryConsoleErr := loginConsoleUser(client)
	if ok, why := loginRetryAllowed(retryState, retryScreenErr, retryConsoleErr); !ok {
		return fmt.Errorf("login unverified, not retyping password: %s", why)
	}

	fmt.Println("Still at login screen - trying to click user and retry...")
	if err := client.MouseClick(0.5, 0.78); err != nil {
		fmt.Printf("warning: mouse click failed: %v\n", err)
	}
	time.Sleep(500 * time.Millisecond)

	if err := client.TypeText(creds.Password); err != nil {
		return fmt.Errorf("type password (retry): %w", err)
	}
	time.Sleep(500 * time.Millisecond)

	if err := client.SendKey(36); err != nil {
		return fmt.Errorf("press return (retry): %w", err)
	}

	if err := waitForLoginConsoleUser(client, creds.Username, loginConsoleUserTimeout, true); err != nil {
		_, finalState, screenErr := client.DetectScreen()
		if screenErr != nil {
			return fmt.Errorf("login failed: %w; final screen detection: %v", err, screenErr)
		}
		return fmt.Errorf("login failed: %w; final screen state: %s", err, finalState)
	}

	fmt.Println("Login successful after retry")
	return nil
}

// confirmLoginTypeAllowed samples the screen and the console user until it has
// conclusive evidence that typing the cached password is safe, or gives up.
//
// It is the last gate before any keystroke: callers may have their own
// corroboration, but tryLoginFallback must be safe on its own.
func confirmLoginTypeAllowed(client *ControlClient, force bool) error {
	streak := 0
	why := "no screen samples taken"
	for i := 0; i < loginScreenConfirmations; i++ {
		if i > 0 {
			time.Sleep(loginScreenConfirmInterval)
		}
		_, state, screenErr := client.DetectScreen()
		user, consoleErr := loginConsoleUser(client)
		if screenErr == nil && state == ScreenStateLoginScreen {
			streak++
		} else {
			streak = 0
		}
		// A caller that already confirmed a desktop without a console user
		// (the login-screen watchdog) has conclusive evidence of its own.
		if force && screenErr == nil && state == ScreenStateDesktop && errors.Is(consoleErr, controlserver.ErrNoConsoleUser) {
			return nil
		}
		var action loginTypeAction
		action, why = loginTypeDecision(state, screenErr, user, consoleErr, streak)
		switch action {
		case loginTypeType:
			return nil
		case loginTypeAbort:
			return fmt.Errorf("not typing password: %s", why)
		}
	}
	return fmt.Errorf("not typing password: %s", why)
}

// loginRetryAllowed reports whether it is safe to type the cached password
// again after a login attempt failed to confirm, given a freshly sampled
// screen state and the error from a fresh console-user query.
//
// Typing is allowed only on conclusive evidence that nobody is logged in: a
// login screen, or a desktop classification the guest agent confirms has no
// console user. An unreadable screen or an inconclusive console-user error
// (agent not connected, exec timeout) means unknown, and unknown must never
// put a plaintext password on the keyboard.
func loginRetryAllowed(state ScreenState, screenErr, consoleErr error) (bool, string) {
	if screenErr != nil {
		return false, fmt.Sprintf("screen state unknown: %v", screenErr)
	}
	switch {
	case consoleErr == nil:
		// Somebody is logged in; the screen classification is irrelevant.
		return false, "a console user is logged in"
	case state == ScreenStateLoginScreen:
		return true, ""
	case state == ScreenStateDesktop && errors.Is(consoleErr, controlserver.ErrNoConsoleUser):
		return true, ""
	case state == ScreenStateDesktop:
		return false, "desktop reached; console user not conclusively empty"
	default:
		return false, fmt.Sprintf("screen state is %s", state)
	}
}

func waitForLoginConsoleUser(client *ControlClient, username string, timeout time.Duration, stopAtLoginScreen bool) error {
	deadline := time.Now().Add(timeout)
	loginScreenDeadline := time.Now().Add(loginScreenRetryDelay)
	var lastErr error
	for time.Now().Before(deadline) {
		user, err := loginConsoleUser(client)
		if err == nil {
			if user == username {
				return nil
			}
			return fmt.Errorf("console user is %q, want %q", user, username)
		}
		lastErr = err

		if stopAtLoginScreen && time.Now().After(loginScreenDeadline) {
			if _, state, err := client.DetectScreen(); err == nil && state == ScreenStateLoginScreen {
				return fmt.Errorf("still at login screen: %w", lastErr)
			}
		}
		time.Sleep(1 * time.Second)
	}
	if lastErr != nil {
		return fmt.Errorf("console user did not become %q: %w", username, lastErr)
	}
	return fmt.Errorf("console user did not become %q", username)
}

func loginConsoleUser(client *ControlClient) (string, error) {
	result, err := client.AgentExecTypedTimeout([]string{"stat", "-f", "%Su %u", "/dev/console"}, nil, "", 8*time.Second)
	if err != nil {
		return "", fmt.Errorf("query console user: %w", err)
	}
	return loginConsoleUserFromExec(result)
}

func loginConsoleUserFromExec(result *controlpb.AgentExecResponse) (string, error) {
	if result == nil {
		return "", fmt.Errorf("query console user: missing response")
	}
	if result.ExitCode != 0 {
		msg := strings.TrimSpace(result.Stderr)
		if msg == "" {
			msg = strings.TrimSpace(result.Stdout)
		}
		if msg == "" {
			msg = "unknown error"
		}
		return "", fmt.Errorf("query console user: %s", msg)
	}
	user, _, err := controlserver.ParseConsoleOwnerOutput(result.Stdout)
	return user, err
}
