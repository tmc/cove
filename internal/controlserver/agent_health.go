// AgentHealthState is the snapshot of agent health observed by the
// proactive monitor. It is read both by the bridge's RPC handlers
// and by the cove status command (which builds one by hand).
package controlserver

import (
	"fmt"
	"strings"
	"time"
)

// startupAgentHealthInterval is the cadence the monitor uses while
// either agent is still coming up. After both daemon and user agent
// are connected the monitor switches to the configured steady
// interval.
const startupAgentHealthInterval = 2 * time.Second

// GUISession describes a guest-side GUI login session observed by the
// agent health probe.
type GUISession struct {
	ID   string
	User string
	Seat string
	Kind string
}

// AgentHealthState tracks proactive agent health monitoring.
type AgentHealthState struct {
	DaemonStatus     string // "connected", "disconnected", "reconnecting"
	UserStatus       string // "connected", "disconnected", "unknown"
	GUISession       GUISession
	GUISessionActive bool
	LastPing         time.Time // last successful daemon ping
	DisconnectAt     time.Time // first ping failure since the last successful ping; zero when connected
	LastErr          string    // last ping error (empty if healthy)
	Version          string    // agent version from last successful ping
	VersionChecked   bool      // true after first version comparison
	UpgradeAttempted bool      // true after auto-upgrade attempt
}

// AgentHealthSummary returns a short status string suitable for UI
// display. Used by the cove status command path that builds an
// AgentHealthState by hand and never connects through a bridge.
func AgentHealthSummary(h AgentHealthState) string {
	return AgentHealthSummaryWithNeverConnected(h, "Agent: not installed")
}

// AgentHealthSummaryWithNeverConnected is the bridge-aware variant of
// AgentHealthSummary; it lets callers customize the pre-first-connect
// label based on the VM's agent config (fresh install vs. expected vs.
// not configured).
func AgentHealthSummaryWithNeverConnected(h AgentHealthState, neverConnected string) string {
	switch h.DaemonStatus {
	case "connected":
		parts := []string{"daemon connected"}
		if h.GUISessionActive {
			parts = append(parts, formatGUISessionSummary(h.GUISession))
		}
		if h.UserStatus == "disconnected" {
			parts = append(parts, "user agent unavailable")
		}
		return strings.Join(parts, "; ")
	case "reconnecting":
		return "Agent: reconnecting..."
	case "disconnected":
		if h.LastPing.IsZero() {
			return neverConnected
		}
		return "Agent: disconnected"
	default:
		// No health check has run yet.
		if h.LastPing.IsZero() {
			return neverConnected
		}
		return "Agent: connecting..."
	}
}

func formatGUISessionSummary(session GUISession) string {
	if session.Seat == "console" && session.Kind == "console" {
		return fmt.Sprintf("GUI session active (user=%s, console)", session.User)
	}
	return fmt.Sprintf("GUI session active (user=%s, seat=%s, %s)", session.User, session.Seat, session.Kind)
}
