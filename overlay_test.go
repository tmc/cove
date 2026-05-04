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

func TestBootOverlayReadyToFade(t *testing.T) {
	tests := []struct {
		summary string
		want    bool
	}{
		{summary: "Agent: connected", want: true},
		{summary: "Agent: connected (no user session)", want: true},
		{summary: "daemon connected; GUI session active (user=desk, seat=seat0, wayland); user agent unavailable", want: true},
		{summary: "Agent: connecting...", want: false},
		{summary: "Agent: reconnecting...", want: false},
		{summary: "Agent: starting (first boot)", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.summary, func(t *testing.T) {
			if got := bootOverlayReadyToFade(tt.summary); got != tt.want {
				t.Fatalf("bootOverlayReadyToFade(%q) = %v, want %v", tt.summary, got, tt.want)
			}
		})
	}
}
