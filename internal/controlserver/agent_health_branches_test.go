package controlserver

import (
	"testing"
	"time"
)

func TestAgentHealthSummaryWithNeverConnectedBranches(t *testing.T) {
	past := time.Unix(1700000000, 0)
	tests := []struct {
		name           string
		state          AgentHealthState
		neverConnected string
		want           string
	}{
		{
			name:           "reconnecting",
			state:          AgentHealthState{DaemonStatus: "reconnecting"},
			neverConnected: "Agent: not installed",
			want:           "Agent: reconnecting...",
		},
		{
			name:           "disconnected never connected",
			state:          AgentHealthState{DaemonStatus: "disconnected"},
			neverConnected: "Agent: fresh install",
			want:           "Agent: fresh install",
		},
		{
			name:           "disconnected after prior ping",
			state:          AgentHealthState{DaemonStatus: "disconnected", LastPing: past},
			neverConnected: "Agent: fresh install",
			want:           "Agent: disconnected",
		},
		{
			name:           "default empty status never connected",
			state:          AgentHealthState{},
			neverConnected: "Agent: not configured",
			want:           "Agent: not configured",
		},
		{
			name:           "default empty status connecting",
			state:          AgentHealthState{LastPing: past},
			neverConnected: "Agent: not configured",
			want:           "Agent: connecting...",
		},
		{
			name:           "connected plain",
			state:          AgentHealthState{DaemonStatus: "connected", UserStatus: "connected"},
			neverConnected: "Agent: not installed",
			want:           "daemon connected",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AgentHealthSummaryWithNeverConnected(tt.state, tt.neverConnected)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAgentHealthSummaryDelegatesToNeverConnectedVariant(t *testing.T) {
	// AgentHealthSummary is the convenience wrapper; verify it threads the
	// default "Agent: not installed" label through for the never-connected
	// disconnected branch.
	got := AgentHealthSummary(AgentHealthState{DaemonStatus: "disconnected"})
	if got != "Agent: not installed" {
		t.Errorf("got %q, want %q", got, "Agent: not installed")
	}
}

func TestFormatGUISessionSummaryConsoleBranch(t *testing.T) {
	got := formatGUISessionSummary(GUISession{User: "alice", Seat: "console", Kind: "console"})
	want := "GUI session active (user=alice, console)"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
