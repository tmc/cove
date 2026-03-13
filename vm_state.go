package main

import (
	"strings"

	vz "github.com/tmc/apple/virtualization"
)

// vmStateLabel maps virtualization state enums to stable lowercase labels.
func vmStateLabel(state vz.VZVirtualMachineState) string {
	switch state {
	case vz.VZVirtualMachineStateRunning:
		return "running"
	case vz.VZVirtualMachineStatePaused:
		return "paused"
	case vz.VZVirtualMachineStateStarting:
		return "starting"
	case vz.VZVirtualMachineStatePausing:
		return "pausing"
	case vz.VZVirtualMachineStateResuming:
		return "resuming"
	case vz.VZVirtualMachineStateStopping:
		return "stopping"
	case vz.VZVirtualMachineStateSaving:
		return "saving"
	case vz.VZVirtualMachineStateRestoring:
		return "restoring"
	case vz.VZVirtualMachineStateError:
		return "error"
	case vz.VZVirtualMachineStateStopped:
		return "stopped"
	default:
		return strings.ToLower(strings.TrimSpace(state.String()))
	}
}

// canonicalVMState normalizes state strings from status responses into labels
// like "running", regardless of whether the source uses enum names.
func canonicalVMState(state string) string {
	s := strings.ToLower(strings.TrimSpace(state))
	s = strings.TrimPrefix(s, "vzvirtualmachinestate")
	return strings.TrimSpace(s)
}
