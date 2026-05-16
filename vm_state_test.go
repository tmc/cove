package main

import (
	"testing"

	vz "github.com/tmc/apple/virtualization"
)

func TestCanonicalVMState(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "running", want: "running"},
		{in: "VZVirtualMachineStateRunning", want: "running"},
		{in: " VZVirtualMachineStateStarting ", want: "starting"},
	}
	for _, tt := range tests {
		if got := canonicalVMState(tt.in); got != tt.want {
			t.Fatalf("canonicalVMState(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestVMStateLabel(t *testing.T) {
	tests := []struct {
		state vz.VZVirtualMachineState
		want  string
	}{
		{vz.VZVirtualMachineStateRunning, "running"},
		{vz.VZVirtualMachineStatePaused, "paused"},
		{vz.VZVirtualMachineStateStarting, "starting"},
		{vz.VZVirtualMachineStatePausing, "pausing"},
		{vz.VZVirtualMachineStateResuming, "resuming"},
		{vz.VZVirtualMachineStateStopping, "stopping"},
		{vz.VZVirtualMachineStateSaving, "saving"},
		{vz.VZVirtualMachineStateRestoring, "restoring"},
		{vz.VZVirtualMachineStateError, "error"},
		{vz.VZVirtualMachineStateStopped, "stopped"},
	}
	for _, tt := range tests {
		if got := vmStateLabel(tt.state); got != tt.want {
			t.Errorf("vmStateLabel(%v) = %q, want %q", tt.state, got, tt.want)
		}
	}
}
