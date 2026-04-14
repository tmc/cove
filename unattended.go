// unattended.go - Unattended macOS install orchestrator
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// runUnattendedSetup runs post-install Setup Assistant automation.
//
// The primary path uses disk injection (SkipSetupAssistant + AutoLogin),
// which bypasses Setup Assistant entirely. This function handles the
// fallback case where injection failed or wasn't used, automating
// Setup Assistant via OCR + keyboard navigation.
//
// It can also run user-provided boot command scripts for custom automation.
func runUnattendedSetup(cs *ControlServer) error {
	ocr := NewOCRService(verbose)
	debugDir := ""
	if debugOCR {
		debugDir = filepath.Join(vmDir, "debug")
		fmt.Printf("OCR debug screenshots will be saved to: %s\n", debugDir)
	}

	// If an automation script file is provided, use that.
	if bootCommandsFile != "" {
		return runAutomationScript(cs, debugDir)
	}

	// Otherwise, run the default unattended flow
	return runDefaultUnattendedFlow(cs, ocr, debugDir)
}

// runAutomationScript loads and executes a vzscript automation file.
func runAutomationScript(cs *ControlServer, debugDir string) error {
	data, err := os.ReadFile(bootCommandsFile)
	if err != nil {
		return fmt.Errorf("read automation script: %w", err)
	}

	if !isVZScriptAutomationFile(bootCommandsFile, data) {
		return unsupportedAutomationScriptError(bootCommandsFile)
	}
	fmt.Printf("Executing vzscript automation from %s\n", bootCommandsFile)

	restoreBackends := forceBootCommandAutomationBackends(cs)
	defer restoreBackends()

	cfg := vzscriptConfig{
		socketPath: GetControlSocketPath(),
		verbose:    verbose,
		controlSrv: cs,
	}
	_ = debugDir
	return runVZScript(data, filepath.Base(bootCommandsFile), cfg)
}

func forceBootCommandAutomationBackends(cs *ControlServer) func() {
	if cs == nil {
		return func() {}
	}

	prevCapture := cs.captureBackend()
	prevInput := cs.inputBackend()
	if prevCapture != automationBackendFramebuffer {
		if verbose {
			fmt.Printf("[unattended] forcing boot command capture backend: %s -> %s\n", prevCapture, automationBackendFramebuffer)
		}
		cs.setCaptureBackend(automationBackendFramebuffer)
	}
	if prevInput != automationBackendFramebuffer {
		if verbose {
			fmt.Printf("[unattended] forcing boot command input backend: %s -> %s\n", prevInput.inputString(), automationBackendFramebuffer.inputString())
		}
		cs.setInputBackend(automationBackendFramebuffer)
	}

	return func() {
		if cs.captureBackend() != prevCapture {
			if verbose {
				fmt.Printf("[unattended] restoring boot command capture backend: %s\n", prevCapture)
			}
			cs.setCaptureBackend(prevCapture)
		}
		if cs.inputBackend() != prevInput {
			if verbose {
				fmt.Printf("[unattended] restoring boot command input backend: %s\n", prevInput.inputString())
			}
			cs.setInputBackend(prevInput)
		}
	}
}

// runDefaultUnattendedFlow waits for the VM to reach a usable state.
//
// Strategy:
//  1. Wait for the screen to stabilize (boot complete)
//  2. Check if we're at desktop (injection succeeded) — done
//  3. Check if we're at login screen — type password, done
//  4. Check if we're at Setup Assistant — run OCR-guided navigation
func runDefaultUnattendedFlow(cs *ControlServer, ocr *OCRService, debugDir string) error {
	fmt.Println("Waiting for VM to boot...")

	// Wait up to 5 minutes for the screen to leave black/Apple logo
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		img, errMsg := cs.captureDisplayImage()
		if errMsg != "" {
			time.Sleep(2 * time.Second)
			continue
		}

		state := DetectScreenStateOCR(img, ocr)
		if verbose {
			fmt.Printf("[unattended] screen state: %s\n", state)
		}

		if debugOCR && debugDir != "" {
			observations, _ := ocr.RecognizeText(img)
			saveOCRDebugScreenshot(img, observations, debugDir, fmt.Sprintf("boot-%s", state))
		}

		switch state {
		case ScreenStateDesktop:
			fmt.Println("VM reached desktop — setup complete!")
			return nil

		case ScreenStateLoginScreen:
			fmt.Println("VM at login screen — attempting login...")
			return attemptLogin(cs, ocr)

		case ScreenStateSetupAssistant:
			fmt.Println("VM at Setup Assistant — running OCR-guided navigation...")
			return runOCRSetupAssistant(cs, ocr, debugDir)

		case ScreenStateBlack, ScreenStateAppleLogo:
			// Still booting
			time.Sleep(3 * time.Second)
			continue

		default:
			time.Sleep(2 * time.Second)
		}
	}

	return fmt.Errorf("timeout waiting for VM to boot")
}

// attemptLogin types the provisioning password at the login screen.
func attemptLogin(cs *ControlServer, ocr *OCRService) error {
	if provisionPassword == "" {
		return fmt.Errorf("at login screen but no -provision-password set")
	}

	// Click in the password field area, then type password
	time.Sleep(500 * time.Millisecond)
	resp := cs.typeText(&controlpb.TextCommand{Text: provisionPassword})
	if !resp.Success {
		return fmt.Errorf("type password: %s", resp.Error)
	}

	time.Sleep(200 * time.Millisecond)
	useCGEvent := cs.inputBackend() == automationBackendWindow
	resp = cs.sendKeyEvent(&controlpb.KeyCommand{KeyCode: 36, KeyDown: true, UseCgEvent: useCGEvent}) // Return
	cs.sendKeyEvent(&controlpb.KeyCommand{KeyCode: 36, KeyDown: false, UseCgEvent: useCGEvent})

	// Wait for desktop
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)
		img, errMsg := cs.captureDisplayImage()
		if errMsg != "" {
			continue
		}
		if DetectScreenStateOCR(img, ocr) == ScreenStateDesktop {
			fmt.Println("Login successful — at desktop!")
			return nil
		}
	}

	return fmt.Errorf("timeout waiting for desktop after login")
}

// runOCRSetupAssistant navigates Setup Assistant using OCR text detection.
// This is the fallback path when disk injection didn't skip Setup Assistant.
func runOCRSetupAssistant(cs *ControlServer, ocr *OCRService, debugDir string) error {
	fmt.Println("Using OCR-driven Setup Assistant navigation...")
	sa := NewSetupAssistantInProcess(cs, ocr, ProvisionConfig{
		Username: provisionUser,
		Password: provisionPassword,
		Admin:    provisionAdmin,
	}, verbose, debugDir)
	return sa.Run()
}
