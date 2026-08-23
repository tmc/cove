package main

import (
	"os"
	"testing"

	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/cove/internal/vmrun"
)

func TestBootOverlayMessage(t *testing.T) {
	oldBootCreds := bootLoginScreenCredentials
	oldBootMode := currentBootSessionMode()
	defer func() {
		bootLoginScreenCredentials = oldBootCreds
		setActiveBootSessionMode(oldBootMode)
	}()

	target := vmSelection{Directory: t.TempDir(), Name: "test"}
	rc := vmrun.RunConfig{}
	setActiveBootSessionMode(bootSessionModeNormal)
	bootLoginScreenCredentials = loginScreenCredentials{}
	title, subtitle, hold := bootOverlayMessageForRun(rc, target)
	if title != "Booting..." || subtitle != "" || hold {
		t.Fatalf("bootOverlayMessage() = %q, %q, %v", title, subtitle, hold)
	}

	bootLoginScreenCredentials = loginScreenCredentials{Username: "u", Password: "p"}
	title, subtitle, hold = bootOverlayMessageForRun(rc, target)
	if title != "Preparing macOS" || subtitle == "" || !hold {
		t.Fatalf("bootOverlayMessage() with creds = %q, %q, %v", title, subtitle, hold)
	}

	bootLoginScreenCredentials = loginScreenCredentials{}
	rc.Unattended = true
	title, subtitle, hold = bootOverlayMessageForRun(rc, target)
	if title != "Preparing macOS" || subtitle == "" || !hold {
		t.Fatalf("bootOverlayMessage() unattended = %q, %q, %v", title, subtitle, hold)
	}

	// A VM with an injected provision pending (the default `cove up` path) holds
	// the overlay through first-boot account creation and the auto-login reboot,
	// even without explicit provision credentials on this run.
	rc.Unattended = false
	if err := os.WriteFile(target.injectSucceededMarker(), []byte("ok"), 0644); err != nil {
		t.Fatalf("write inject marker: %v", err)
	}
	title, subtitle, hold = bootOverlayMessageForRun(rc, target)
	if title != "Preparing macOS" || subtitle == "" || !hold {
		t.Fatalf("bootOverlayMessage() injected first boot = %q, %q, %v", title, subtitle, hold)
	}
	// Resuming a saved suspend state means the guest is already past first boot,
	// so the overlay must not be held over a live session.
	if err := os.WriteFile(suspendStatePathForVM(target.Directory), []byte("state"), 0644); err != nil {
		t.Fatalf("write suspend state: %v", err)
	}
	title, subtitle, hold = bootOverlayMessageForRun(rc, target)
	if title != "Booting..." || subtitle != "" || hold {
		t.Fatalf("bootOverlayMessage() resume = %q, %q, %v", title, subtitle, hold)
	}
	if err := os.Remove(suspendStatePathForVM(target.Directory)); err != nil {
		t.Fatalf("remove suspend state: %v", err)
	}
	if err := os.Remove(target.injectSucceededMarker()); err != nil {
		t.Fatalf("remove inject marker: %v", err)
	}

	setActiveBootSessionMode(bootSessionModeRecovery)
	title, subtitle, hold = bootOverlayMessageForRun(rc, target)
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
			wantSubtitle: "Writing system files... 42%",
		},
		{
			name:         "restoring clamps high",
			phase:        installOverlayRestoring,
			percent:      123,
			wantTitle:    "Installing macOS",
			wantSubtitle: "Writing system files... 100%",
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

func TestPauseOverlayLabel(t *testing.T) {
	tests := []struct {
		name  string
		state vz.VZVirtualMachineState
		want  string
		show  bool
	}{
		{"paused", vz.VZVirtualMachineStatePaused, "Paused", true},
		{"saving", vz.VZVirtualMachineStateSaving, "Saving...", true},
		{"restoring", vz.VZVirtualMachineStateRestoring, "Restoring...", true},
		{"running", vz.VZVirtualMachineStateRunning, "", false},
		{"resuming", vz.VZVirtualMachineStateResuming, "", false},
		{"starting", vz.VZVirtualMachineStateStarting, "", false},
		{"stopped", vz.VZVirtualMachineStateStopped, "", false},
		{"error", vz.VZVirtualMachineStateError, "", false},
		{"unobserved", vz.VZVirtualMachineState(-1), "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, show := pauseOverlayLabel(tt.state)
			if got != tt.want || show != tt.show {
				t.Fatalf("pauseOverlayLabel(%v) = %q, %v; want %q, %v", tt.state, got, show, tt.want, tt.show)
			}
		})
	}
}

// TestPauseOverlayReconcileClearsRestoring models the run-loop reconcile: an
// overlay put up during Restoring must come down once the VM reports Running,
// even if the intermediate Paused and Resuming samples are never observed.
func TestPauseOverlayReconcileClearsRestoring(t *testing.T) {
	var shown string
	reconcile := func(state vz.VZVirtualMachineState) {
		label, want := pauseOverlayLabel(state)
		if !want {
			shown = ""
			return
		}
		shown = label
	}
	reconcile(vz.VZVirtualMachineStateRestoring)
	if shown != "Restoring..." {
		t.Fatalf("during restore = %q, want %q", shown, "Restoring...")
	}
	// Restoring -> Running with every intermediate sample coalesced away.
	reconcile(vz.VZVirtualMachineStateRunning)
	if shown != "" {
		t.Fatalf("after resume = %q, want no overlay", shown)
	}
}
