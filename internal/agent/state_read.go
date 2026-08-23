package agent

import "github.com/tmc/cove/internal/vmconfig"

// Status returns the persisted guest-agent state for the VM, or nil if the
// VM has no recorded agent configuration.
func Status(vmDirectory string) *vmconfig.AgentConfig {
	cfg, err := vmconfig.Load(vmDirectory)
	if err != nil || cfg == nil {
		return nil
	}
	return CloneConfig(cfg.Agent)
}

// Verified reports whether a guest-agent connection has ever succeeded for
// the VM. A later failure to connect to a verified agent is transient, not
// evidence that the agent is missing.
func Verified(vmDirectory string) bool {
	st := Status(vmDirectory)
	return st != nil && st.Verified
}
