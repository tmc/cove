package main

import (
	"fmt"
	"path/filepath"
	"time"
)

// runProvisioningAutomation starts the Setup Assistant automation
// This is called in a goroutine after the VM starts with GUI mode
// It handles multiple scenarios:
// 1. Setup Assistant appears → use keyboard automation to create user
// 2. Login Screen appears → try to login with configured password (LaunchDaemon may have created user)
// 3. Desktop appears → already provisioned, nothing to do
func runProvisioningAutomation(socketPath string) {
	fmt.Println("\n=== Starting Auto-Provisioning ===")
	fmt.Printf("Username: %s\n", provisionUser)
	fmt.Printf("Admin: %v\n", provisionAdmin)
	fmt.Println()

	// Create setup assistant with configured options
	assistant := NewSetupAssistant(SetupAssistantOptions{
		SocketPath: socketPath,
		Username:   provisionUser,
		Password:   provisionPassword,
		Admin:      provisionAdmin,
		Verbose:    verbose,
		SaveDir:    filepath.Join(vmDir, "provision_screenshots"),
	})

	// Run the automation - this handles Setup Assistant
	if err := assistant.Run(); err != nil {
		fmt.Printf("Setup Assistant automation failed: %v\n", err)
		fmt.Println("Attempting login screen fallback...")

		// Fallback: try to login if we're at login screen
		// This handles the case where LaunchDaemon created user but auto-login failed
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

// tryLoginFallback attempts to login at the login screen using keyboard automation
// This is a fallback when LaunchDaemon creates the user but auto-login doesn't work
func tryLoginFallback(socketPath string) error {
	client := NewControlClient(socketPath)

	// Wait for connection
	if err := client.WaitForConnection(30 * time.Second); err != nil {
		return fmt.Errorf("control socket not available: %w", err)
	}

	// Take screenshot to check screen state
	_, state, err := client.DetectScreen()
	if err != nil {
		return fmt.Errorf("screen detection failed: %w", err)
	}

	if state != ScreenStateLoginScreen {
		return fmt.Errorf("not at login screen (current state: %s)", state)
	}

	fmt.Println("Detected login screen - attempting keyboard login...")

	// Type the password (assuming the password field is focused)
	if err := client.TypeText(provisionPassword); err != nil {
		return fmt.Errorf("type password: %w", err)
	}

	time.Sleep(500 * time.Millisecond)

	// Press Return to submit
	if err := client.SendKey(36); err != nil { // 36 = Return
		return fmt.Errorf("press return: %w", err)
	}

	// Wait for login to complete
	time.Sleep(5 * time.Second)

	// Check if we made it to desktop
	_, newState, err := client.DetectScreen()
	if err != nil {
		return fmt.Errorf("screen detection after login: %w", err)
	}

	if newState == ScreenStateDesktop {
		fmt.Println("Login successful - reached desktop")
		return nil
	}

	// Still at login screen - might need to click the user first
	fmt.Println("Still at login screen - trying to click user and retry...")

	// Click in center of screen to select user (heuristic)
	if err := client.SendMouseClick(300, 300); err != nil {
		fmt.Printf("Warning: mouse click failed: %v\n", err)
	}
	time.Sleep(500 * time.Millisecond)

	// Type password again
	if err := client.TypeText(provisionPassword); err != nil {
		return fmt.Errorf("type password (retry): %w", err)
	}
	time.Sleep(500 * time.Millisecond)

	// Press Return
	if err := client.SendKey(36); err != nil {
		return fmt.Errorf("press return (retry): %w", err)
	}

	time.Sleep(5 * time.Second)

	// Final check
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
