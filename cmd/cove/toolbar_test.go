package main

import (
	"testing"

	"github.com/tmc/apple/appkit"
	"github.com/tmc/apple/objc"
	vz "github.com/tmc/apple/virtualization"
)

func TestIsModalResponseOK(t *testing.T) {
	tests := []struct {
		name string
		in   appkit.NSModalResponse
		want bool
	}{
		{"ok", 1, true},
		{"cancel", 0, false},
		{"stop", -1000, false},
		{"abort", -1001, false},
		{"alert-first-button", 1000, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isModalResponseOK(tt.in); got != tt.want {
				t.Errorf("isModalResponseOK(%d) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestToolbarItemIDs(t *testing.T) {
	got := toolbarItemIDs()
	if len(got) == 0 {
		t.Fatal("toolbarItemIDs empty")
	}
	want := []string{
		toolbarIDStop, toolbarIDStartPause, toolbarIDRestart, toolbarIDBootOptions,
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("position %d: got %q, want %q", i, got[i], w)
		}
	}
	seen := map[string]bool{}
	for _, id := range got {
		if id == "" {
			t.Error("empty identifier in toolbarItemIDs")
		}
		if seen[id] {
			t.Errorf("duplicate identifier %q", id)
		}
		seen[id] = true
	}
	for _, id := range []string{toolbarIDCaptureInput, toolbarIDScreenshot, toolbarIDSharedFolder} {
		if !seen[id] {
			t.Errorf("missing trailing identifier %q", id)
		}
	}
}

func TestValidateVMMenuItem(t *testing.T) {
	tests := []struct {
		name           string
		action         objc.SEL
		state          vz.VZVirtualMachineState
		captureEnabled bool
		canSuspend     bool
		installing     bool
		wantTitle      string
		wantEnabled    bool
		wantHandled    bool
	}{
		// startPauseVM:
		{
			name:        "startPause running",
			action:      selStartPauseVM,
			state:       vz.VZVirtualMachineStateRunning,
			wantTitle:   "Pause",
			wantEnabled: true,
			wantHandled: true,
		},
		{
			name:        "startPause paused becomes resume",
			action:      selStartPauseVM,
			state:       vz.VZVirtualMachineStatePaused,
			wantTitle:   "Resume",
			wantEnabled: true,
			wantHandled: true,
		},
		{
			name:        "startPause stopped",
			action:      selStartPauseVM,
			state:       vz.VZVirtualMachineStateStopped,
			wantTitle:   "Start",
			wantEnabled: true,
			wantHandled: true,
		},
		{
			name:        "startPause error",
			action:      selStartPauseVM,
			state:       vz.VZVirtualMachineStateError,
			wantTitle:   "Start",
			wantEnabled: true,
			wantHandled: true,
		},
		{
			name:        "startPause starting is busy",
			action:      selStartPauseVM,
			state:       vz.VZVirtualMachineStateStarting,
			wantTitle:   "starting",
			wantEnabled: false,
			wantHandled: true,
		},
		{
			name:        "startPause stopping is busy",
			action:      selStartPauseVM,
			state:       vz.VZVirtualMachineStateStopping,
			wantTitle:   "stopping",
			wantEnabled: false,
			wantHandled: true,
		},
		{
			name:        "startPause saving is busy",
			action:      selStartPauseVM,
			state:       vz.VZVirtualMachineStateSaving,
			wantTitle:   "saving",
			wantEnabled: false,
			wantHandled: true,
		},
		{
			name:        "startPause restoring is busy",
			action:      selStartPauseVM,
			state:       vz.VZVirtualMachineStateRestoring,
			wantTitle:   "restoring",
			wantEnabled: false,
			wantHandled: true,
		},
		{
			name:        "startPause installing disabled",
			action:      selStartPauseVM,
			state:       vz.VZVirtualMachineStateRunning,
			installing:  true,
			wantTitle:   "Pause",
			wantEnabled: false,
			wantHandled: true,
		},

		// suspendVM:
		{
			name:        "suspend running supported",
			action:      selSuspendVM,
			state:       vz.VZVirtualMachineStateRunning,
			canSuspend:  true,
			wantEnabled: true,
			wantHandled: true,
		},
		{
			name:        "suspend paused supported",
			action:      selSuspendVM,
			state:       vz.VZVirtualMachineStatePaused,
			canSuspend:  true,
			wantEnabled: true,
			wantHandled: true,
		},
		{
			name:        "suspend running unsupported",
			action:      selSuspendVM,
			state:       vz.VZVirtualMachineStateRunning,
			canSuspend:  false,
			wantEnabled: false,
			wantHandled: true,
		},
		{
			name:        "suspend stopped",
			action:      selSuspendVM,
			state:       vz.VZVirtualMachineStateStopped,
			canSuspend:  false,
			wantEnabled: false,
			wantHandled: true,
		},
		{
			name:        "suspend installing disabled",
			action:      selSuspendVM,
			state:       vz.VZVirtualMachineStateRunning,
			canSuspend:  true,
			installing:  true,
			wantEnabled: false,
			wantHandled: true,
		},

		// restartVM:
		{
			name:        "restart running",
			action:      selRestartVM,
			state:       vz.VZVirtualMachineStateRunning,
			wantEnabled: true,
			wantHandled: true,
		},
		{
			name:        "restart paused disabled",
			action:      selRestartVM,
			state:       vz.VZVirtualMachineStatePaused,
			wantEnabled: false,
			wantHandled: true,
		},
		{
			name:        "restart stopped disabled",
			action:      selRestartVM,
			state:       vz.VZVirtualMachineStateStopped,
			wantEnabled: false,
			wantHandled: true,
		},
		{
			name:        "restart busy disabled",
			action:      selRestartVM,
			state:       vz.VZVirtualMachineStateStarting,
			wantEnabled: false,
			wantHandled: true,
		},
		{
			name:        "restart installing disabled",
			action:      selRestartVM,
			state:       vz.VZVirtualMachineStateRunning,
			installing:  true,
			wantEnabled: false,
			wantHandled: true,
		},

		// stopVM:
		{
			name:        "stop running",
			action:      selStopVM,
			state:       vz.VZVirtualMachineStateRunning,
			wantEnabled: true,
			wantHandled: true,
		},
		{
			name:        "stop paused",
			action:      selStopVM,
			state:       vz.VZVirtualMachineStatePaused,
			wantEnabled: true,
			wantHandled: true,
		},
		{
			name:        "stop stopped disabled",
			action:      selStopVM,
			state:       vz.VZVirtualMachineStateStopped,
			wantEnabled: false,
			wantHandled: true,
		},
		{
			name:        "stop busy disabled",
			action:      selStopVM,
			state:       vz.VZVirtualMachineStateStopping,
			wantEnabled: false,
			wantHandled: true,
		},
		{
			name:        "stop installing disabled",
			action:      selStopVM,
			state:       vz.VZVirtualMachineStateRunning,
			installing:  true,
			wantEnabled: false,
			wantHandled: true,
		},

		// captureInput:
		{
			name:           "captureInput not captured",
			action:         selCaptureInput,
			state:          vz.VZVirtualMachineStateRunning,
			captureEnabled: false,
			wantTitle:      "Capture Input",
			wantEnabled:    true,
			wantHandled:    true,
		},
		{
			name:           "captureInput captured shows release hint",
			action:         selCaptureInput,
			state:          vz.VZVirtualMachineStateRunning,
			captureEnabled: true,
			wantTitle:      "Release Input (Ctrl-Opt)",
			wantEnabled:    true,
			wantHandled:    true,
		},
		{
			name:           "captureInput paused disabled",
			action:         selCaptureInput,
			state:          vz.VZVirtualMachineStatePaused,
			captureEnabled: false,
			wantTitle:      "Capture Input",
			wantEnabled:    false,
			wantHandled:    true,
		},
		{
			name:           "captureInput stopped disabled",
			action:         selCaptureInput,
			state:          vz.VZVirtualMachineStateStopped,
			captureEnabled: false,
			wantTitle:      "Capture Input",
			wantEnabled:    false,
			wantHandled:    true,
		},
		{
			name:           "captureInput installing disabled",
			action:         selCaptureInput,
			state:          vz.VZVirtualMachineStateRunning,
			installing:     true,
			wantTitle:      "Capture Input",
			wantEnabled:    false,
			wantHandled:    true,
		},

		// takeScreenshot:
		{
			name:        "screenshot running",
			action:      selTakeScreenshot,
			state:       vz.VZVirtualMachineStateRunning,
			wantEnabled: true,
			wantHandled: true,
		},
		{
			name:        "screenshot paused disabled",
			action:      selTakeScreenshot,
			state:       vz.VZVirtualMachineStatePaused,
			wantEnabled: false,
			wantHandled: true,
		},
		{
			name:        "screenshot stopped disabled",
			action:      selTakeScreenshot,
			state:       vz.VZVirtualMachineStateStopped,
			wantEnabled: false,
			wantHandled: true,
		},
		{
			name:        "screenshot busy disabled",
			action:      selTakeScreenshot,
			state:       vz.VZVirtualMachineStateSaving,
			wantEnabled: false,
			wantHandled: true,
		},

		// bootRecovery:
		{
			name:        "bootRecovery running",
			action:      selBootRecovery,
			state:       vz.VZVirtualMachineStateRunning,
			wantEnabled: true,
			wantHandled: true,
		},
		{
			name:        "bootRecovery paused",
			action:      selBootRecovery,
			state:       vz.VZVirtualMachineStatePaused,
			wantEnabled: true,
			wantHandled: true,
		},
		{
			name:        "bootRecovery stopped",
			action:      selBootRecovery,
			state:       vz.VZVirtualMachineStateStopped,
			wantEnabled: true,
			wantHandled: true,
		},
		{
			name:        "bootRecovery busy disabled",
			action:      selBootRecovery,
			state:       vz.VZVirtualMachineStateStarting,
			wantEnabled: false,
			wantHandled: true,
		},
		{
			name:        "bootRecovery installing disabled",
			action:      selBootRecovery,
			state:       vz.VZVirtualMachineStateStopped,
			installing:  true,
			wantEnabled: false,
			wantHandled: true,
		},

		// Shared folders:
		{
			name:        "addSharedFolder not busy",
			action:      selAddSharedFolder,
			state:       vz.VZVirtualMachineStateRunning,
			wantEnabled: true,
			wantHandled: true,
		},
		{
			name:        "addSharedFolder busy disabled",
			action:      selAddSharedFolder,
			state:       vz.VZVirtualMachineStateStarting,
			wantEnabled: false,
			wantHandled: true,
		},
		{
			name:        "removeSharedFolder not busy",
			action:      selRemoveSharedFolder,
			state:       vz.VZVirtualMachineStateRunning,
			wantEnabled: true,
			wantHandled: true,
		},
		{
			name:        "removeAllSharedFolders not busy",
			action:      selRemoveAllSharedFolders,
			state:       vz.VZVirtualMachineStateRunning,
			wantEnabled: true,
			wantHandled: true,
		},

		// Unhandled action:
		{
			name:        "unknown action unhandled",
			action:      objc.Sel("unknownAction:"),
			state:       vz.VZVirtualMachineStateRunning,
			wantEnabled: true,
			wantHandled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			title, enabled, handled := validateVMMenuItem(tt.action, tt.state, tt.captureEnabled, tt.canSuspend, tt.installing)
			if handled != tt.wantHandled {
				t.Errorf("handled = %v, want %v", handled, tt.wantHandled)
			}
			if enabled != tt.wantEnabled {
				t.Errorf("enabled = %v, want %v", enabled, tt.wantEnabled)
			}
			if title != tt.wantTitle {
				t.Errorf("title = %q, want %q", title, tt.wantTitle)
			}
		})
	}
}

func TestVMToolbarCanSuspend(t *testing.T) {
	oldCanSaveRestore := canSaveRestore
	defer func() { canSaveRestore = oldCanSaveRestore }()

	tb := &VMToolbar{}

	canSaveRestore = false
	if got := tb.canSuspend(vz.VZVirtualMachineStateRunning); got {
		t.Errorf("canSuspend with canSaveRestore=false returned true")
	}

	canSaveRestore = true
	if got := tb.canSuspend(vz.VZVirtualMachineStateStopped); got {
		t.Errorf("canSuspend on stopped VM returned true")
	}

	if got := tb.canSuspend(vz.VZVirtualMachineStateRunning); !got {
		t.Errorf("canSuspend on running VM with canSaveRestore=true returned false")
	}

	if got := tb.canSuspend(vz.VZVirtualMachineStatePaused); !got {
		t.Errorf("canSuspend on paused VM with canSaveRestore=true returned false")
	}
}

func TestVMToolbarValidateMenuItem(t *testing.T) {
	tb := &VMToolbar{
		state: vz.VZVirtualMachineStatePaused,
	}

	item := appkit.NewMenuItemWithTitleActionKeyEquivalent("Pause", selStartPauseVM, "")
	enabled := tb.validateMenuItem(0, 0, item.ID)
	if !enabled {
		t.Errorf("expected validateMenuItem for startPause on paused VM to be enabled")
	}
	if got := item.Title(); got != "Resume" {
		t.Errorf("expected item title to be 'Resume', got %q", got)
	}

	// Capture input when running and captureEnabled=true
	tb.state = vz.VZVirtualMachineStateRunning
	tb.captureEnabled = true
	captureItem := appkit.NewMenuItemWithTitleActionKeyEquivalent("Capture Input", selCaptureInput, "")
	enabled = tb.validateMenuItem(0, 0, captureItem.ID)
	if !enabled {
		t.Errorf("expected validateMenuItem for captureInput on running VM to be enabled")
	}
	if got := captureItem.Title(); got != "Release Input (Ctrl-Opt)" {
		t.Errorf("expected captureItem title to be 'Release Input (Ctrl-Opt)', got %q", got)
	}
}
