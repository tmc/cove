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
	oldBootMode := currentBootSessionMode()
	setActiveBootSessionMode(bootSessionModeNormal)
	defer setActiveBootSessionMode(oldBootMode)

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

func TestBootOverlayReadyToFadeRecovery(t *testing.T) {
	oldBootMode := currentBootSessionMode()
	setActiveBootSessionMode(bootSessionModeRecovery)
	defer setActiveBootSessionMode(oldBootMode)

	if !bootOverlayReadyToFade("Agent: starting (first boot)") {
		t.Fatal("bootOverlayReadyToFade in recovery mode = false, want true")
	}
}

func TestInstallOverlayMessage(t *testing.T) {
	tests := []struct {
		name         string
		phase        installOverlayPhase
		percent      float64
		wantTitle    string
		wantSubtitle string
	}{
		{
			name:         "starting",
			phase:        installOverlayStarting,
			wantTitle:    "Starting installation...",
			wantSubtitle: "Allocating disk image.",
		},
		{
			name:         "restoring",
			phase:        installOverlayRestoring,
			percent:      42.4,
			wantTitle:    "Installing macOS",
			wantSubtitle: "Restoring system files... 42%",
		},
		{
			name:         "restoring clamps high",
			phase:        installOverlayRestoring,
			percent:      123,
			wantTitle:    "Installing macOS",
			wantSubtitle: "Restoring system files... 100%",
		},
		{
			name:         "first boot",
			phase:        installOverlayFirstBoot,
			wantTitle:    "Installing macOS",
			wantSubtitle: "First boot in progress...",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, subtitle, hold := installOverlayMessage(tt.phase, tt.percent)
			if title != tt.wantTitle || subtitle != tt.wantSubtitle || !hold {
				t.Fatalf("installOverlayMessage() = %q, %q, %v; want %q, %q, true", title, subtitle, hold, tt.wantTitle, tt.wantSubtitle)
			}
		})
	}
}
