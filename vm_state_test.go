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
	if got := vmStateLabel(vz.VZVirtualMachineStateRunning); got != "running" {
		t.Fatalf("vmStateLabel(running) = %q, want %q", got, "running")
	}
	if got := vmStateLabel(vz.VZVirtualMachineStateStopped); got != "stopped" {
		t.Fatalf("vmStateLabel(stopped) = %q, want %q", got, "stopped")
	}
}
