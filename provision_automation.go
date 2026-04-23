package main

import (
	"fmt"
	"path/filepath"
	"time"
)

// runProvisioningAutomation starts the Setup Assistant automation using
// the ControlServer directly (in-process) for reliable HID-based input.
//
// It waits for the VM window to be capturable before starting, since the
// ControlServer needs windowNum and viewContentHeight to take screenshots.
func runProvisioningAutomation(cs *ControlServer) {
	fmt.Println("\n=== Starting Auto-Provisioning (GUI) ===")
	fmt.Printf("Username: %s\n", provisionUser)
	fmt.Printf("Admin: %v\n", provisionAdmin)
	fmt.Println()
	vmDirectory := cs.effectiveVMDir()

	prevInputBackend := cs.inputBackend()
	targetInputBackend := prevInputBackend
	if targetInputBackend == automationBackendAuto {
		if cs.window.ID != 0 {
			targetInputBackend = automationBackendWindow
		} else {
			targetInputBackend = automationBackendFramebuffer
		}
	}
	cs.setInputBackend(targetInputBackend)
	defer cs.setInputBackend(prevInputBackend)

	// Wait for the VM window to be ready for screenshots.
	// The ControlServer is already initialized with SetVMViewWithWindow,
	// but the VM may still be booting and the window not yet rendered.
	if err := waitForVMScreenReady(cs, 120*time.Second); err != nil {
		fmt.Printf("warning: VM screen not ready: %v\n", err)
		fmt.Println("Continuing anyway — automation may recover during page detection.")
	}

	ocr := NewOCRService(verbose)
	debugDir := filepath.Join(vmDirectory, "provision_screenshots")
	if debugOCR {
		fmt.Printf("OCR debug screenshots: %s\n", debugDir)
	}

	assistant := NewSetupAssistantInProcess(cs, ocr, ProvisionConfig{
		Username: provisionUser,
		Password: provisionPassword,
		Fullname: provisionUser,
		Admin:    provisionAdmin,
	}, verbose, debugDir)

	// Run the automation
	if err := assistant.Run(); err != nil {
		fmt.Printf("Setup Assistant automation failed: %v\n", err)
		fmt.Println("Attempting login screen fallback...")

		socketPath := GetControlSocketPathForVM(vmDirectory)
		creds := loginScreenCredentials{Username: provisionUser, Password: provisionPassword}
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
		fmt.Printf("User '%s' has been created.\n", provisionUser)
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
	if err := waitForVMScreenReady(cs, 120*time.Second); err != nil {
		if verbose {
			fmt.Printf("[login-watchdog] VM screen not ready: %v\n", err)
		}
		return
	}

	socketPath := GetControlSocketPathForVM(cs.effectiveVMDir())
	client := NewControlClient(socketPath)

	deadline := time.Now().Add(3 * time.Minute)
	loggedAtLogin := false
	for time.Now().Before(deadline) {
		_, state, err := client.DetectScreen()
		if err != nil {
			time.Sleep(3 * time.Second)
			continue
		}
		if state == ScreenStateDesktop {
			if _, _, err := cs.consoleUser(); err != nil {
				if verbose {
					fmt.Printf("[login-watchdog] desktop classification but no console user: %v\n", err)
				}
				state = ScreenStateLoginScreen
			} else {
				if verbose {
					fmt.Println("[login-watchdog] desktop reached, exiting")
				}
				return
			}
		}
		if state == ScreenStateLoginScreen {
			if !loggedAtLogin {
				fmt.Println("\n=== Login screen detected — typing cached password ===")
				loggedAtLogin = true
			}
			if err := tryLoginFallback(socketPath, creds, true); err != nil {
				if verbose {
					fmt.Printf("[login-watchdog] login attempt: %v\n", err)
				}
				time.Sleep(5 * time.Second)
				continue
			}
			return
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

	_, state, err := client.DetectScreen()
	if err != nil {
		return fmt.Errorf("screen detection failed: %w", err)
	}

	if state != ScreenStateLoginScreen && !(force && state == ScreenStateDesktop) {
		return fmt.Errorf("not at login screen (current state: %s)", state)
	}

	if force && state == ScreenStateDesktop {
		fmt.Println("Desktop classified without a console user - attempting keyboard login...")
	} else {
		fmt.Println("Detected login screen - attempting keyboard login...")
	}

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
	time.Sleep(5 * time.Second)

	_, newState, err := client.DetectScreen()
	if err != nil {
		return fmt.Errorf("screen detection after login: %w", err)
	}

	if newState == ScreenStateDesktop {
		fmt.Println("Login successful - reached desktop")
		return nil
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
	time.Sleep(5 * time.Second)

	_, finalState, err := client.DetectScreen()
	if err != nil {
		return fmt.Errorf("final screen detection: %w", err)
	}

	if finalState != ScreenStateDesktop {
		return fmt.Errorf("login failed - still at %s", finalState)
	}

	fmt.Println("Login successful after retry")
	return nil
}
