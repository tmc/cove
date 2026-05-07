package controlserver

import (
	"testing"
	"time"
)

func TestAgentHealthSummaryWithLinuxGUISession(t *testing.T) {
	h := AgentHealthState{
		DaemonStatus:     "connected",
		UserStatus:       "disconnected",
		GUISession:       GUISession{User: "desk", Seat: "seat0", Kind: "wayland"},
		GUISessionActive: true,
	}
	want := "daemon connected; GUI session active (user=desk, seat=seat0, wayland); user agent unavailable"
	if got := AgentHealthSummary(h); got != want {
		t.Fatalf("AgentHealthSummary() = %q, want %q", got, want)
	}
}

func TestAgentHealthSummaryWithMacOSGUISession(t *testing.T) {
	h := AgentHealthState{
		DaemonStatus:     "connected",
		UserStatus:       "connected",
		GUISession:       GUISession{User: "me", Seat: "console", Kind: "console"},
		GUISessionActive: true,
	}
	want := "daemon connected; GUI session active (user=me, console)"
	if got := AgentHealthSummary(h); got != want {
		t.Fatalf("AgentHealthSummary() = %q, want %q", got, want)
	}
}

func TestAgentHealthSummaryConnectedUserDisconnected(t *testing.T) {
	h := AgentHealthState{DaemonStatus: "connected", UserStatus: "disconnected"}
	want := "daemon connected; user agent unavailable"
	if got := AgentHealthSummary(h); got != want {
		t.Fatalf("AgentHealthSummary() = %q, want %q", got, want)
	}
}

func TestMarkAgentReconnectingRecordsFirstFailure(t *testing.T) {
	b := &AgentBridge{}

	b.MarkAgentReconnecting("first failure")
	first := b.health.DisconnectAt
	if first.IsZero() {
		t.Fatal("first MarkAgentReconnecting should set DisconnectAt")
	}
	if b.health.DaemonStatus != "reconnecting" {
		t.Errorf("DaemonStatus = %q, want reconnecting", b.health.DaemonStatus)
	}

	time.Sleep(2 * time.Millisecond)
	b.MarkAgentReconnecting("second failure")
	if !b.health.DisconnectAt.Equal(first) {
		t.Errorf("DisconnectAt moved from %v to %v on second call", first, b.health.DisconnectAt)
	}
	if b.health.LastErr != "second failure" {
		t.Errorf("LastErr = %q, want updated", b.health.LastErr)
	}
}

func TestMarkAgentConnectedClearsDisconnectEdge(t *testing.T) {
	b := &AgentBridge{}

	b.MarkAgentReconnecting("ping failed")
	if b.health.DisconnectAt.IsZero() {
		t.Fatal("setup: DisconnectAt should be set")
	}

	b.MarkAgentConnected("v0.1.0")
	if !b.health.DisconnectAt.IsZero() {
		t.Errorf("DisconnectAt = %v, want zero after recovery", b.health.DisconnectAt)
	}
	if b.health.DaemonStatus != "connected" {
		t.Errorf("DaemonStatus = %q, want connected", b.health.DaemonStatus)
	}
	if b.health.Version != "v0.1.0" {
		t.Errorf("Version = %q, want v0.1.0", b.health.Version)
	}
	if b.health.LastPing.IsZero() {
		t.Errorf("LastPing should be updated to now")
	}
	if b.health.LastErr != "" {
		t.Errorf("LastErr = %q, want cleared", b.health.LastErr)
	}
}

func TestMarkAgentConnectedComputesDowntime(t *testing.T) {
	b := &AgentBridge{}
	b.healthMu.Lock()
	b.health.DisconnectAt = time.Now().Add(-50 * time.Millisecond)
	b.health.DaemonStatus = "reconnecting"
	b.healthMu.Unlock()

	b.MarkAgentConnected("v1")
	if !b.health.DisconnectAt.IsZero() {
		t.Errorf("DisconnectAt should be cleared after recovery")
	}
	if b.health.LastPing.IsZero() {
		t.Errorf("LastPing should be updated to now")
	}
}

func TestSetHealthStatusTracksDisconnectEdge(t *testing.T) {
	b := &AgentBridge{}

	b.SetHealthStatus("disconnected", "", "vm not running")
	first := b.health.DisconnectAt
	if first.IsZero() {
		t.Fatal("SetHealthStatus(disconnected) should record DisconnectAt")
	}

	time.Sleep(2 * time.Millisecond)
	b.SetHealthStatus("disconnected", "", "still not running")
	if !b.health.DisconnectAt.Equal(first) {
		t.Errorf("DisconnectAt changed under repeated disconnected: %v -> %v", first, b.health.DisconnectAt)
	}

	b.SetHealthStatus("connected", "v1", "")
	if !b.health.DisconnectAt.IsZero() {
		t.Errorf("DisconnectAt = %v, want zero after connected", b.health.DisconnectAt)
	}
}
