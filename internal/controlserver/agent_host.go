package controlserver

import (
	"context"
	"net"
	"time"

	vz "github.com/tmc/apple/virtualization"
	agentstate "github.com/tmc/cove/internal/agent"
)

// AgentHost is the narrow back-channel the agent sub-component needs
// from the ControlServer facade. It hides VM-specific plumbing (the
// vsock device manager, the VM dispatch queue) and host-side globals
// (the lifecycle clock, the auto-upgrade decision) so the bridge can
// live in this package without importing package main.
//
// All methods are called from within Agent. Implementations live in
// package main and adapt the existing ControlServer fields.
type AgentHost interface {
	// VMDir returns the per-VM state directory. Used for vmconfig
	// lookups and platform detection.
	VMDir() string

	// VMState reports the current VZVirtualMachine state. The bridge
	// uses this to decide whether the agent is reachable.
	VMState() (vz.VZVirtualMachineState, error)

	// Linux reports whether the guest is a Linux VM. Some bridge code
	// paths (user-agent bootstrap, sshd args) branch on this.
	Linux() bool

	// Now returns the current time, routed through the host's
	// lifecycle clock so tests can inject a fake clock.
	Now() time.Time

	// DialAgent opens a vsock connection to the guest agent on the
	// given port. Wraps the vsock device manager so the bridge does
	// not depend on VM internals.
	DialAgent(ctx context.Context, port uint32) (net.Conn, error)

	// LifecycleContext returns the active lifecycle context. The
	// bridge uses it to bound the health monitor goroutine.
	LifecycleContext() context.Context

	// Running reports whether the control server is still running.
	// The health monitor exits when it returns false.
	Running() bool

	// MaybeAutoUpgradeAgent decides whether to auto-upgrade the guest
	// agent given the observed guest version. Folds the host-version
	// lookup, sandbox capability check, and upgrade invocation behind
	// one method so the bridge stays free of those package-main
	// globals. The implementation logs and runs the upgrade
	// asynchronously when applicable; it returns true once an upgrade
	// has been kicked off, false otherwise. The reset callback is
	// invoked from the upgrade goroutine after a successful upgrade so
	// the bridge can clear its versionChecked flag.
	MaybeAutoUpgradeAgent(guestVersion string, onUpgraded func()) bool

	// TimeoutContext returns a context derived from the lifecycle
	// context with the given timeout.
	TimeoutContext(timeout time.Duration) (context.Context, context.CancelFunc)

	// ProbeGUISession runs a guest-side probe and returns the active
	// GUI session, if any. The host dispatches by guest OS.
	ProbeGUISession(ctx context.Context, client *agentstate.AgentClient) (GUISession, bool, error)

	// LaunchAgentArtifact returns the label and plist payload used
	// when bootstrapping the per-user launch agent on macOS guests.
	LaunchAgentArtifact() (label string, plist string)
}
