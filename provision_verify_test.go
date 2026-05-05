package main

import (
	"errors"
	"reflect"
	"testing"

	agentstate "github.com/tmc/vz-macos/internal/agent"
	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

func TestAgentExecExitOK(t *testing.T) {
	tests := []struct {
		name string
		resp *controlpb.ControlResponse
		err  error
		want bool
	}{
		{
			name: "exit zero",
			resp: agentExecVerifyResponse(0),
			want: true,
		},
		{
			name: "exit nonzero",
			resp: agentExecVerifyResponse(1),
			want: false,
		},
		{
			name: "transport success without exec result",
			resp: &controlpb.ControlResponse{Success: true},
			want: false,
		},
		{
			name: "control failure",
			resp: &controlpb.ControlResponse{Success: false, Error: "agent failed"},
			want: false,
		},
		{
			name: "send error",
			err:  errors.New("dial failed"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agentExecExitOK(tt.resp, tt.err)
			if got != tt.want {
				t.Fatalf("agentExecExitOK() = %v, want %v", got, tt.want)
			}
		})
	}
}

func agentExecVerifyResponse(exitCode int32) *controlpb.ControlResponse {
	return &controlpb.ControlResponse{
		Success: true,
		Result: &controlpb.ControlResponse_AgentExecResult{
			AgentExecResult: &controlpb.AgentExecResponse{ExitCode: exitCode},
		},
	}
}

func TestVerifyRunningGuestProbesLinux(t *testing.T) {
	probes := verifyRunningGuestProbes(agentstate.PlatformLinux)
	descs := make([]string, 0, len(probes))
	for _, probe := range probes {
		descs = append(descs, probe.desc)
	}
	wantDescs := []string{
		"Agent binary",
		"Agent systemd unit",
		"Agent systemd service",
		"Provisioning completed marker",
		"vz-agent process",
	}
	if !reflect.DeepEqual(descs, wantDescs) {
		t.Fatalf("probe descs = %#v, want %#v", descs, wantDescs)
	}
	for _, probe := range probes {
		if probe.desc == "Agent LaunchDaemon" {
			t.Fatalf("linux probes include macOS LaunchDaemon: %#v", probes)
		}
	}
	if got, want := probes[1].args, []string{"test", "-f", "/etc/systemd/system/vz-agent.service"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("systemd unit args = %#v, want %#v", got, want)
	}
	if got, want := probes[2].args, []string{"systemctl", "is-active", "vz-agent"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("systemd active args = %#v, want %#v", got, want)
	}
	if got := probes[3].args; len(got) != 3 || got[0] != "sh" || got[1] != "-lc" {
		t.Fatalf("marker args = %#v, want shell probe", got)
	}
}

func TestVerifyRunningGuestProbesMacOS(t *testing.T) {
	probes := verifyRunningGuestProbes(agentstate.PlatformMacOS)
	var found bool
	for _, probe := range probes {
		if probe.desc == "Agent LaunchDaemon" {
			found = true
			if got, want := probe.args, []string{"test", "-f", "/Library/LaunchDaemons/com.github.tmc.vz-macos.vz-agent.plist"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("launchdaemon args = %#v, want %#v", got, want)
			}
		}
	}
	if !found {
		t.Fatal("macOS probes missing Agent LaunchDaemon")
	}
}
