package main

import (
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
		state    vz.VZVirtualMachineState
		title    string
		busy     bool
		enabled  bool
		prefix   string
	}{
		{vz.VZVirtualMachineStateRunning, "Pause", false, true, "Rn:"},
		{vz.VZVirtualMachineStatePaused, "Resume", false, true, "Pa:"},
		{vz.VZVirtualMachineStateStopped, "Start", false, true, "St:"},
		{vz.VZVirtualMachineStateStarting, vmStateName(vz.VZVirtualMachineStateStarting), true, false, "Up:"},
		{vz.VZVirtualMachineStateStopping, vmStateName(vz.VZVirtualMachineStateStopping), true, false, "Dn:"},
		{vz.VZVirtualMachineStateSaving, vmStateName(vz.VZVirtualMachineStateSaving), true, false, "Sv:"},
		{vz.VZVirtualMachineStateRestoring, vmStateName(vz.VZVirtualMachineStateRestoring), true, false, "Rs:"},
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
