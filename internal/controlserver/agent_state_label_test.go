package controlserver

import (
	"testing"

	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/vz-macos/internal/vmstate"
)

func TestVMStateLabel(t *testing.T) {
	tests := []struct {
		name  string
		state vz.VZVirtualMachineState
		want  string
	}{
		{"stopped", vz.VZVirtualMachineStateStopped, "stopped"},
		{"running", vz.VZVirtualMachineStateRunning, "running"},
		{"starting", vz.VZVirtualMachineStateStarting, "starting"},
		{"pausing", vz.VZVirtualMachineStatePausing, "pausing"},
		{"paused", vz.VZVirtualMachineStatePaused, "paused"},
		{"resuming", vz.VZVirtualMachineStateResuming, "resuming"},
		{"stopping", vz.VZVirtualMachineStateStopping, "stopping"},
		{"saving", vz.VZVirtualMachineStateSaving, "saving"},
		{"restoring", vz.VZVirtualMachineStateRestoring, "restoring"},
		{"error", vz.VZVirtualMachineStateError, "error"},
		{"unknown high", vz.VZVirtualMachineState(99), "state(99)"},
		{"unknown negative", vz.VZVirtualMachineState(-1), "state(-1)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := vmstate.Label(tt.state)
			if got != tt.want {
				t.Errorf("vmstate.Label(%d) = %q, want %q", int(tt.state), got, tt.want)
			}
		})
	}
}
