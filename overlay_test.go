package main

import "testing"

func TestBootOverlayMessage(t *testing.T) {
	oldUnattended := unattended
	oldProvisionUser := provisionUser
	oldProvisionPassword := provisionPassword
	oldProvisionStrategy := provisionStrategy
	oldInstallVM := installVM
	oldBootCreds := bootLoginScreenCredentials
	oldBootMode := currentBootSessionMode()
	defer func() {
		unattended = oldUnattended
		provisionUser = oldProvisionUser
		provisionPassword = oldProvisionPassword
		provisionStrategy = oldProvisionStrategy
		installVM = oldInstallVM
		bootLoginScreenCredentials = oldBootCreds
		setActiveBootSessionMode(oldBootMode)
	}()

	setActiveBootSessionMode(bootSessionModeNormal)
	unattended = false
	provisionUser = ""
	provisionPassword = ""
	provisionStrategy = ""
	installVM = false
	bootLoginScreenCredentials = loginScreenCredentials{}
	title, subtitle, hold := bootOverlayMessage()
	if title != "Booting..." || subtitle != "" || hold {
		t.Fatalf("bootOverlayMessage() = %q, %q, %v", title, subtitle, hold)
	}

	bootLoginScreenCredentials = loginScreenCredentials{Username: "u", Password: "p"}
	title, subtitle, hold = bootOverlayMessage()
	if title != "Preparing macOS" || subtitle == "" || !hold {
		t.Fatalf("bootOverlayMessage() with creds = %q, %q, %v", title, subtitle, hold)
	}

	bootLoginScreenCredentials = loginScreenCredentials{}
	unattended = true
	title, subtitle, hold = bootOverlayMessage()
	if title != "Preparing macOS" || subtitle == "" || !hold {
		t.Fatalf("bootOverlayMessage() unattended = %q, %q, %v", title, subtitle, hold)
	}

	setActiveBootSessionMode(bootSessionModeRecovery)
	title, subtitle, hold = bootOverlayMessage()
	if title != "Booting..." || subtitle != "" || hold {
		t.Fatalf("bootOverlayMessage() recovery = %q, %q, %v", title, subtitle, hold)
	}
}
