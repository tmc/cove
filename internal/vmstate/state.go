// Package vmstate normalizes Virtualization.framework VM state labels.
package vmstate

import (
	"fmt"
	"strings"

	vz "github.com/tmc/apple/virtualization"
)

// Label maps virtualization state enums to stable lowercase labels.
func Label(state vz.VZVirtualMachineState) string {
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
		return fmt.Sprintf("state(%d)", int(state))
	}
}

// Canonical normalizes status response strings into stable lowercase labels.
func Canonical(state string) string {
	s := strings.ToLower(strings.TrimSpace(state))
	s = strings.TrimPrefix(s, "vzvirtualmachinestate")
	return strings.TrimSpace(s)
}
