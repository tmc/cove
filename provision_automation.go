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

	// Wait for the VM window to be ready for screenshots.
	// The ControlServer is already initialized with SetVMViewWithWindow,
	// but the VM may still be booting and the window not yet rendered.
	if err := waitForVMScreenReady(cs, 120*time.Second); err != nil {
		fmt.Printf("warning: VM screen not ready: %v\n", err)
		fmt.Println("Continuing anyway — automation may recover during page detection.")
	}

	ocr := NewOCRService(verbose)
	debugDir := filepath.Join(vmDir, "provision_screenshots")
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

		socketPath := GetControlSocketPathForVM(cs.effectiveVMDir())
		if loginErr := tryLoginFallback(socketPath); loginErr != nil {
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

// tryLoginFallback attempts to login at the login screen using keyboard automation.
func tryLoginFallback(socketPath string) error {
	client := NewControlClient(socketPath)

	if err := client.WaitForConnection(30 * time.Second); err != nil {
		return fmt.Errorf("control socket not available: %w", err)
	}

	_, state, err := client.DetectScreen()
	if err != nil {
		return fmt.Errorf("screen detection failed: %w", err)
	}

	if state != ScreenStateLoginScreen {
		return fmt.Errorf("not at login screen (current state: %s)", state)
	}

	fmt.Println("Detected login screen - attempting keyboard login...")

	if err := client.TypeText(provisionPassword); err != nil {
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
	if err := client.SendMouseClick(300, 300); err != nil {
		fmt.Printf("warning: mouse click failed: %v\n", err)
	}
	time.Sleep(500 * time.Millisecond)

	if err := client.TypeText(provisionPassword); err != nil {
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
