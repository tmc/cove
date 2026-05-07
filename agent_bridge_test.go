package main

import (
	"testing"
	"time"
)

// TestAgentBridgeMarkReconnectingRecordsFirstFailure verifies the
// disconnect-edge invariant directly on the bridge: the first failure
// stamps disconnectAt, repeated failures within the same streak do
// not move it. This guards the downtime accounting that
// markAgentConnected reports on recovery.
func TestAgentBridgeMarkReconnectingRecordsFirstFailure(t *testing.T) {
	b := &agentBridge{}

	b.markAgentReconnecting("first")
	first := b.health.disconnectAt
	if first.IsZero() {
		t.Fatal("first markAgentReconnecting should stamp disconnectAt")
	}
	if b.health.daemonStatus != "reconnecting" {
		t.Errorf("daemonStatus = %q, want reconnecting", b.health.daemonStatus)
	}

	time.Sleep(2 * time.Millisecond)
	b.markAgentReconnecting("second")
	if !b.health.disconnectAt.Equal(first) {
		t.Errorf("disconnectAt moved on repeated reconnect: %v -> %v", first, b.health.disconnectAt)
	}
	if b.health.lastErr != "second" {
		t.Errorf("lastErr = %q, want %q", b.health.lastErr, "second")
	}
}

// TestAgentBridgeMarkConnectedClearsDisconnectEdge verifies that the
// recovery edge clears disconnectAt and refreshes lastPing on the
// bridge directly.
func TestAgentBridgeMarkConnectedClearsDisconnectEdge(t *testing.T) {
	b := &agentBridge{}

	b.markAgentReconnecting("ping failed")
	if b.health.disconnectAt.IsZero() {
		t.Fatal("setup: disconnectAt should be set")
	}

	b.markAgentConnected("v1.2.3")
	if !b.health.disconnectAt.IsZero() {
		t.Errorf("disconnectAt = %v, want zero after recovery", b.health.disconnectAt)
	}
	if b.health.daemonStatus != "connected" {
		t.Errorf("daemonStatus = %q, want connected", b.health.daemonStatus)
	}
	if b.health.version != "v1.2.3" {
		t.Errorf("version = %q, want v1.2.3", b.health.version)
	}
	if b.health.lastPing.IsZero() {
		t.Errorf("lastPing should be refreshed")
	}
	if b.health.lastErr != "" {
		t.Errorf("lastErr = %q, want cleared", b.health.lastErr)
	}
}

// TestAgentBridgeNextIntervalUsesStartupUntilConnected verifies the
// poll-cadence invariant: the bridge stays on the short startup
// cadence until both daemon and user agent are connected, then
// switches to the steady cadence.
func TestAgentBridgeNextIntervalUsesStartupUntilConnected(t *testing.T) {
	b := &agentBridge{}

	if got := b.nextAgentHealthInterval(time.Minute); got != startupAgentHealthInterval {
		t.Fatalf("zero state interval = %v, want %v", got, startupAgentHealthInterval)
	}

	b.health.daemonStatus = "connected"
	if got := b.nextAgentHealthInterval(time.Minute); got != startupAgentHealthInterval {
		t.Fatalf("daemon-only interval = %v, want %v", got, startupAgentHealthInterval)
	}

	b.health.userStatus = "connected"
	if got := b.nextAgentHealthInterval(time.Minute); got != time.Minute {
		t.Fatalf("both connected interval = %v, want 1m", got)
	}
}

// TestAgentBridgeSetHealthStatusTracksDisconnectEdge mirrors the
// setHealthStatus disconnect-edge invariant on the bridge directly.
// setHealthStatus is the path used by callers that bypass markAgent*
// (e.g. healthCheckOnce when the VM is not running) and must keep
// the same edge semantics or downtime accounting drifts.
func TestAgentBridgeSetHealthStatusTracksDisconnectEdge(t *testing.T) {
	b := &agentBridge{}

	b.setHealthStatus("disconnected", "", "vm not running")
	first := b.health.disconnectAt
	if first.IsZero() {
		t.Fatal("setHealthStatus(disconnected) should stamp disconnectAt")
	}

	time.Sleep(2 * time.Millisecond)
	b.setHealthStatus("disconnected", "", "still down")
	if !b.health.disconnectAt.Equal(first) {
		t.Errorf("disconnectAt moved under repeated disconnected: %v -> %v", first, b.health.disconnectAt)
	}

	b.setHealthStatus("connected", "v1", "")
	if !b.health.disconnectAt.IsZero() {
		t.Errorf("disconnectAt = %v, want zero after connected", b.health.disconnectAt)
	}
	if b.health.lastPing.IsZero() {
		t.Errorf("lastPing should be refreshed")
	}
}

// TestAgentBridgeGetAgentWithoutVMReturnsNotConfigured verifies the
// nil-cs degradation path: a bare &agentBridge{} (no parent
// ControlServer) returns the same "vm not configured" error the
// pre-extraction path produced for an unconfigured ControlServer.
// This keeps test constructors that build &ControlServer{} working.
func TestAgentBridgeGetAgentWithoutVMReturnsNotConfigured(t *testing.T) {
	b := &agentBridge{}
	_, err := b.getAgent()
	if err == nil {
		t.Fatal("getAgent on bare bridge should error")
	}
	if got := err.Error(); got != "vm not configured" {
		t.Errorf("err = %q, want %q", got, "vm not configured")
	}
}
