package main

import (
	"strings"
	"testing"

	vz "github.com/tmc/apple/virtualization"
)

func TestAgentUnavailableForVMState(t *testing.T) {
	tests := []struct {
		name      string
		state     vz.VZVirtualMachineState
		wantError string
	}{
		{
			name:      "running",
			state:     vz.VZVirtualMachineStateRunning,
			wantError: "",
		},
		{
			name:      "starting",
			state:     vz.VZVirtualMachineStateStarting,
			wantError: "still booting",
		},
		{
			name:      "paused",
			state:     vz.VZVirtualMachineStatePaused,
			wantError: "paused",
		},
		{
			name:      "stopped",
			state:     vz.VZVirtualMachineStateStopped,
			wantError: "vm is stopped",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := agentUnavailableForVMState(tt.state)
			if tt.wantError == "" {
				if err != nil {
					t.Fatalf("agentUnavailableForVMState(%s) error = %v, want nil", tt.state.String(), err)
				}
				return
			}
			if err == nil {
				t.Fatalf("agentUnavailableForVMState(%s) = nil, want error", tt.state.String())
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("agentUnavailableForVMState(%s) = %q, want substring %q", tt.state.String(), err.Error(), tt.wantError)
			}
		})
	}
}
