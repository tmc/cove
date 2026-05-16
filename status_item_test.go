package main

import (
	"strings"
	"testing"

	vz "github.com/tmc/apple/virtualization"
)

func TestTruncateMiddle(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"short unchanged", "abc", 10, "abc"},
		{"exact length", "abcde", 5, "abcde"},
		{"max le 3 truncates head", "abcdef", 3, "abc"},
		{"max le 3 zero", "abcdef", 0, ""},
		{"middle ellipsis even", "abcdefghij", 7, "ab...ij"},
		{"middle ellipsis odd", "abcdefghij", 8, "ab...hij"},
		{"unicode runes counted", "αβγδεζηθικ", 7, "αβ...ικ"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := truncateMiddle(tt.in, tt.max); got != tt.want {
				t.Fatalf("truncateMiddle(%q,%d) = %q, want %q", tt.in, tt.max, got, tt.want)
			}
		})
	}
}

func TestStatusItemRunStateLogic(t *testing.T) {
	c := &VMStatusItemController{}
	tests := []struct {
		state   vz.VZVirtualMachineState
		title   string
		busy    bool
		enabled bool
		prefix  string
	}{
		{vz.VZVirtualMachineStateRunning, "Pause", false, true, "Run:"},
		{vz.VZVirtualMachineStatePaused, "Resume", false, true, "Pau:"},
		{vz.VZVirtualMachineStateStopped, "Start", false, true, "Off:"},
		{vz.VZVirtualMachineStateStarting, "starting", true, false, "Up:"},
		{vz.VZVirtualMachineStateStopping, "stopping", true, false, "Dn:"},
		{vz.VZVirtualMachineStateSaving, "saving", true, false, "Sav:"},
		{vz.VZVirtualMachineStateRestoring, "restoring", true, false, "Res:"},
	}
	for _, tt := range tests {
		t.Run(vmStateName(tt.state), func(t *testing.T) {
			if got := c.runStateTitle(tt.state); got != tt.title {
				t.Errorf("runStateTitle = %q, want %q", got, tt.title)
			}
			if got := c.stateBusy(tt.state); got != tt.busy {
				t.Errorf("stateBusy = %v, want %v", got, tt.busy)
			}
			if got := c.runStateEnabled(tt.state); got != tt.enabled {
				t.Errorf("runStateEnabled = %v, want %v", got, tt.enabled)
			}
			if got := c.buttonStatePrefix(tt.state); got != tt.prefix {
				t.Errorf("buttonStatePrefix = %q, want %q", got, tt.prefix)
			}
		})
	}
}

func TestStatusItemStatePresentation(t *testing.T) {
	tests := []struct {
		name   string
		state  vz.VZVirtualMachineState
		prefix string
		label  string
		busy   bool
	}{
		{"running", vz.VZVirtualMachineStateRunning, "Run:", "running", false},
		{"paused", vz.VZVirtualMachineStatePaused, "Pau:", "paused", false},
		{"stopped", vz.VZVirtualMachineStateStopped, "Off:", "stopped", false},
		{"starting", vz.VZVirtualMachineStateStarting, "Up:", "starting", true},
		{"saving", vz.VZVirtualMachineStateSaving, "Sav:", "saving", true},
		{"error", vz.VZVirtualMachineStateError, "Err:", "error", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := statusItemStatePresentation(tt.state)
			if got.Prefix != tt.prefix || got.Label != tt.label || got.Busy != tt.busy {
				t.Fatalf("presentation = %+v, want prefix=%q label=%q busy=%v", got, tt.prefix, tt.label, tt.busy)
			}
		})
	}
}

func TestStatusItemButtonTitleIsLegibleAndBounded(t *testing.T) {
	c := &VMStatusItemController{name: "very-long-production-runner-name"}
	c.state = vz.VZVirtualMachineStateRunning
	got := c.buttonTitle()
	if !strings.HasPrefix(got, "Run:") {
		t.Fatalf("buttonTitle = %q, want Run prefix", got)
	}
	if len([]rune(got)) > 18 {
		t.Fatalf("buttonTitle = %q is too long", got)
	}
}

func TestStatusItemErrorStateCanRestart(t *testing.T) {
	c := &VMStatusItemController{}
	if title := c.runStateTitle(vz.VZVirtualMachineStateError); title != "Start" {
		t.Fatalf("runStateTitle(error) = %q, want Start", title)
	}
	if !c.runStateEnabled(vz.VZVirtualMachineStateError) {
		t.Fatal("runStateEnabled(error) = false, want true")
	}
}
