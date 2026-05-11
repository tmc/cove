package controlserver

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	vz "github.com/tmc/apple/virtualization"
)

// TestAgentBridgeMarkReconnectingRecordsFirstFailure verifies the
// disconnect-edge invariant on the bridge: the first failure stamps
// DisconnectAt, repeated failures within the same streak do not move
// it.
func TestAgentBridgeMarkReconnectingRecordsFirstFailure(t *testing.T) {
	b := &AgentBridge{}

	b.MarkAgentReconnecting("first")
	first := b.health.DisconnectAt
	if first.IsZero() {
		t.Fatal("first MarkAgentReconnecting should stamp DisconnectAt")
	}
	if b.health.DaemonStatus != "reconnecting" {
		t.Errorf("DaemonStatus = %q, want reconnecting", b.health.DaemonStatus)
	}

	time.Sleep(2 * time.Millisecond)
	b.MarkAgentReconnecting("second")
	if !b.health.DisconnectAt.Equal(first) {
		t.Errorf("DisconnectAt moved on repeated reconnect: %v -> %v", first, b.health.DisconnectAt)
	}
	if b.health.LastErr != "second" {
		t.Errorf("LastErr = %q, want %q", b.health.LastErr, "second")
	}
}

func TestAgentBridgeMarkConnectedClearsDisconnectEdge(t *testing.T) {
	b := &AgentBridge{}

	b.MarkAgentReconnecting("ping failed")
	if b.health.DisconnectAt.IsZero() {
		t.Fatal("setup: DisconnectAt should be set")
	}

	b.MarkAgentConnected("v1.2.3")
	if !b.health.DisconnectAt.IsZero() {
		t.Errorf("DisconnectAt = %v, want zero after recovery", b.health.DisconnectAt)
	}
	if b.health.DaemonStatus != "connected" {
		t.Errorf("DaemonStatus = %q, want connected", b.health.DaemonStatus)
	}
	if b.health.Version != "v1.2.3" {
		t.Errorf("Version = %q, want v1.2.3", b.health.Version)
	}
	if b.health.LastPing.IsZero() {
		t.Errorf("LastPing should be refreshed")
	}
	if b.health.LastErr != "" {
		t.Errorf("LastErr = %q, want cleared", b.health.LastErr)
	}
}

func TestAgentBridgeNextIntervalUsesStartupUntilConnected(t *testing.T) {
	b := &AgentBridge{}

	if got := b.NextAgentHealthInterval(time.Minute); got != startupAgentHealthInterval {
		t.Fatalf("zero state interval = %v, want %v", got, startupAgentHealthInterval)
	}

	b.health.DaemonStatus = "connected"
	if got := b.NextAgentHealthInterval(time.Minute); got != startupAgentHealthInterval {
		t.Fatalf("daemon-only interval = %v, want %v", got, startupAgentHealthInterval)
	}

	b.health.UserStatus = "connected"
	if got := b.NextAgentHealthInterval(time.Minute); got != time.Minute {
		t.Fatalf("both connected interval = %v, want 1m", got)
	}
}

func TestAgentBridgeSetHealthStatusTracksDisconnectEdge(t *testing.T) {
	b := &AgentBridge{}

	b.SetHealthStatus("disconnected", "", "vm not running")
	first := b.health.DisconnectAt
	if first.IsZero() {
		t.Fatal("SetHealthStatus(disconnected) should stamp DisconnectAt")
	}

	time.Sleep(2 * time.Millisecond)
	b.SetHealthStatus("disconnected", "", "still down")
	if !b.health.DisconnectAt.Equal(first) {
		t.Errorf("DisconnectAt moved under repeated disconnected: %v -> %v", first, b.health.DisconnectAt)
	}

	b.SetHealthStatus("connected", "v1", "")
	if !b.health.DisconnectAt.IsZero() {
		t.Errorf("DisconnectAt = %v, want zero after connected", b.health.DisconnectAt)
	}
	if b.health.LastPing.IsZero() {
		t.Errorf("LastPing should be refreshed")
	}
}

// TestAgentBridgeGetAgentWithoutHostReturnsNotConfigured verifies the
// nil-host degradation path: a bare &AgentBridge{} returns the same
// "vm not configured" error the pre-extraction path produced for an
// unconfigured ControlServer.
func TestAgentBridgeGetAgentWithoutHostReturnsNotConfigured(t *testing.T) {
	b := &AgentBridge{}
	_, err := b.GetAgent()
	if err == nil {
		t.Fatal("GetAgent on bare bridge should error")
	}
	if got := err.Error(); got != "vm not configured" {
		t.Errorf("err = %q, want %q", got, "vm not configured")
	}
}

func TestAgentBridgeNilHostAccessors(t *testing.T) {
	_ = t.TempDir()
	tests := []struct {
		name string
		run  func(*AgentBridge) error
	}{
		{"daemon", func(b *AgentBridge) error {
			_, err := b.GetAgent()
			return err
		}},
		{"user", func(b *AgentBridge) error {
			_, err := b.GetUserAgent()
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b AgentBridge
			if err := tt.run(&b); err == nil || err.Error() != "vm not configured" {
				t.Fatalf("err = %v, want vm not configured", err)
			}
			if b.CurrentDaemonClient() != nil {
				t.Fatal("CurrentDaemonClient = non-nil, want nil")
			}
			if !b.LastPing().IsZero() {
				t.Fatal("LastPing = non-zero, want zero")
			}
			if got := b.HealthSnapshot(); got != (AgentHealthState{}) {
				t.Fatalf("HealthSnapshot = %+v, want zero", got)
			}
		})
	}
}

func TestAgentBridgeTimeoutContextWithoutHost(t *testing.T) {
	var b AgentBridge
	ctx, cancel := b.timeoutContext(time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("ctx.Err = %v, want DeadlineExceeded", ctx.Err())
	}
}

func TestAgentUnavailableForVMState(t *testing.T) {
	tests := []struct {
		name      string
		state     vz.VZVirtualMachineState
		wantError string
	}{
		{name: "running", state: vz.VZVirtualMachineStateRunning, wantError: ""},
		{name: "starting", state: vz.VZVirtualMachineStateStarting, wantError: "still booting"},
		{name: "paused", state: vz.VZVirtualMachineStatePaused, wantError: "paused"},
		{name: "stopped", state: vz.VZVirtualMachineStateStopped, wantError: "vm is stopped"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := AgentUnavailableForVMState(tt.state)
			if tt.wantError == "" {
				if err != nil {
					t.Fatalf("AgentUnavailableForVMState(%s) error = %v, want nil", tt.state.String(), err)
				}
				return
			}
			if err == nil {
				t.Fatalf("AgentUnavailableForVMState(%s) = nil, want error", tt.state.String())
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("AgentUnavailableForVMState(%s) = %q, want substring %q", tt.state.String(), err.Error(), tt.wantError)
			}
		})
	}
}

func TestParseConsoleOwnerOutput(t *testing.T) {
	tests := []struct {
		name     string
		stdout   string
		wantUser string
		wantUID  int
		wantErr  bool
	}{
		{name: "user", stdout: "user 501\n", wantUser: "user", wantUID: 501},
		{name: "root", stdout: "root 0\n", wantErr: true},
		{name: "bad uid", stdout: "user nope\n", wantErr: true},
		{name: "bad format", stdout: "user\n", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotUser, gotUID, err := ParseConsoleOwnerOutput(tt.stdout)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseConsoleOwnerOutput(%q): got nil error", tt.stdout)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseConsoleOwnerOutput(%q): %v", tt.stdout, err)
			}
			if gotUser != tt.wantUser || gotUID != tt.wantUID {
				t.Fatalf("ParseConsoleOwnerOutput(%q): got (%q, %d), want (%q, %d)", tt.stdout, gotUser, gotUID, tt.wantUser, tt.wantUID)
			}
		})
	}
}
