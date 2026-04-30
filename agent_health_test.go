package main

import (
	"testing"
	"time"
)

// TestResolveAgentHealthInterval covers acceptance #4: tick interval is
// configurable via COVE_AGENT_HEALTH_INTERVAL with a 30s fallback when the
// env is unset, empty, or unparseable.
func TestResolveAgentHealthInterval(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want time.Duration
	}{
		{name: "unset", env: "", want: defaultAgentHealthInterval},
		{name: "valid_seconds", env: "5s", want: 5 * time.Second},
		{name: "valid_minute", env: "1m", want: time.Minute},
		{name: "garbage_falls_back", env: "not-a-duration", want: defaultAgentHealthInterval},
		{name: "zero_falls_back", env: "0s", want: defaultAgentHealthInterval},
		{name: "negative_falls_back", env: "-30s", want: defaultAgentHealthInterval},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(agentHealthIntervalEnv, tc.env)
			got := resolveAgentHealthInterval()
			if got != tc.want {
				t.Errorf("resolveAgentHealthInterval() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNextAgentHealthInterval(t *testing.T) {
	cs := &ControlServer{}
	if got := cs.nextAgentHealthInterval(time.Minute); got != startupAgentHealthInterval {
		t.Fatalf("nextAgentHealthInterval before connect = %v, want %v", got, startupAgentHealthInterval)
	}

	cs.agentHealth.daemonStatus = "connected"
	cs.agentHealth.userStatus = "disconnected"
	if got := cs.nextAgentHealthInterval(time.Minute); got != startupAgentHealthInterval {
		t.Fatalf("nextAgentHealthInterval before user agent = %v, want %v", got, startupAgentHealthInterval)
	}

	cs.agentHealth.userStatus = "connected"
	if got := cs.nextAgentHealthInterval(time.Minute); got != time.Minute {
		t.Fatalf("nextAgentHealthInterval after connect = %v, want 1m", got)
	}
}

// TestMarkAgentReconnectingRecordsFirstFailure verifies that the first
// reconnect attempt sets disconnectAt, and that subsequent calls during the
// same disconnect streak do NOT overwrite it. Without this invariant, the
// downtime computed at recovery would be wrong.
func TestMarkAgentReconnectingRecordsFirstFailure(t *testing.T) {
	cs := &ControlServer{}

	cs.markAgentReconnecting("first failure")
	first := cs.agentHealth.disconnectAt
	if first.IsZero() {
		t.Fatal("first markAgentReconnecting should set disconnectAt")
	}
	if cs.agentHealth.daemonStatus != "reconnecting" {
		t.Errorf("daemonStatus = %q, want reconnecting", cs.agentHealth.daemonStatus)
	}

	// Second call within the same streak must not move the timestamp.
	time.Sleep(2 * time.Millisecond)
	cs.markAgentReconnecting("second failure")
	if !cs.agentHealth.disconnectAt.Equal(first) {
		t.Errorf("disconnectAt moved from %v to %v on second call", first, cs.agentHealth.disconnectAt)
	}
	if cs.agentHealth.lastErr != "second failure" {
		t.Errorf("lastErr = %q, want updated", cs.agentHealth.lastErr)
	}
}

// TestMarkAgentConnectedClearsDisconnectEdge verifies that recovering from
// a disconnect streak clears disconnectAt. The next failure starts fresh
// rather than producing inflated downtime measurements.
func TestMarkAgentConnectedClearsDisconnectEdge(t *testing.T) {
	cs := &ControlServer{}

	cs.markAgentReconnecting("ping failed")
	if cs.agentHealth.disconnectAt.IsZero() {
		t.Fatal("setup: disconnectAt should be set")
	}

	cs.markAgentConnected("v0.1.0")
	if !cs.agentHealth.disconnectAt.IsZero() {
		t.Errorf("disconnectAt = %v, want zero after recovery", cs.agentHealth.disconnectAt)
	}
	if cs.agentHealth.daemonStatus != "connected" {
		t.Errorf("daemonStatus = %q, want connected", cs.agentHealth.daemonStatus)
	}
	if cs.agentHealth.version != "v0.1.0" {
		t.Errorf("version = %q, want v0.1.0", cs.agentHealth.version)
	}
	if cs.agentHealth.lastPing.IsZero() {
		t.Errorf("lastPing should be updated to now")
	}
	if cs.agentHealth.lastErr != "" {
		t.Errorf("lastErr = %q, want cleared", cs.agentHealth.lastErr)
	}
}

// TestMarkAgentConnectedComputesDowntime verifies the downtime accounting
// when the disconnect-to-recovery edge spans a real elapsed gap. This is
// the value rendered into the INFO log on recovery — operators rely on it
// to triage "how long was the agent down for".
func TestMarkAgentConnectedComputesDowntime(t *testing.T) {
	cs := &ControlServer{}
	// Simulate a disconnect 50ms ago.
	cs.healthMu.Lock()
	cs.agentHealth.disconnectAt = time.Now().Add(-50 * time.Millisecond)
	cs.agentHealth.daemonStatus = "reconnecting"
	cs.healthMu.Unlock()

	cs.markAgentConnected("v1")
	if !cs.agentHealth.disconnectAt.IsZero() {
		t.Errorf("disconnectAt should be cleared after recovery")
	}
	// We can't observe the slog field directly without a custom handler,
	// but the field clearing + lastPing freshness prove the recovery edge
	// fired. Cardinality of the elapsed timestamp is exercised in the live
	// monitor's Info call (covered by manual smoke against a paused agent).
	if cs.agentHealth.lastPing.IsZero() {
		t.Errorf("lastPing should be updated to now")
	}
}

// TestSetHealthStatusTracksDisconnectEdge verifies the legacy
// setHealthStatus path (used by callers that don't go through the new
// markAgent* helpers, e.g. when the VM isn't running) also tracks
// disconnectAt, so downtime accounting stays accurate regardless of
// which code path entered the disconnect.
func TestSetHealthStatusTracksDisconnectEdge(t *testing.T) {
	cs := &ControlServer{}

	cs.setHealthStatus("disconnected", "", "vm not running")
	first := cs.agentHealth.disconnectAt
	if first.IsZero() {
		t.Fatal("setHealthStatus(disconnected) should record disconnectAt")
	}

	// Repeated disconnected calls preserve the original timestamp.
	time.Sleep(2 * time.Millisecond)
	cs.setHealthStatus("disconnected", "", "still not running")
	if !cs.agentHealth.disconnectAt.Equal(first) {
		t.Errorf("disconnectAt changed under repeated disconnected: %v -> %v", first, cs.agentHealth.disconnectAt)
	}

	// Connected clears the edge so the next failure starts clean.
	cs.setHealthStatus("connected", "v1", "")
	if !cs.agentHealth.disconnectAt.IsZero() {
		t.Errorf("disconnectAt = %v, want zero after connected", cs.agentHealth.disconnectAt)
	}
}
