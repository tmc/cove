package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

type erroringGuestTerminalAgent struct {
	err error
}

func (e *erroringGuestTerminalAgent) AgentExecTypedTimeout(args []string, env map[string]string, workDir string, timeout time.Duration) (*controlpb.AgentExecResponse, error) {
	return nil, e.err
}

func TestDetectGuestOSErrorPaths(t *testing.T) {
	tests := []struct {
		name    string
		agent   guestTerminalAgent
		wantSub string
	}{
		{
			name:    "rpc error",
			agent:   &erroringGuestTerminalAgent{err: errors.New("rpc down")},
			wantSub: "detect guest os: rpc down",
		},
		{
			name: "non-zero exit",
			agent: &fakeGuestTerminalAgent{responses: []*controlpb.AgentExecResponse{
				{ExitCode: 2, Stderr: "permission denied"},
			}},
			wantSub: "detect guest os: permission denied",
		},
		{
			name: "unsupported os",
			agent: &fakeGuestTerminalAgent{responses: []*controlpb.AgentExecResponse{
				{Stdout: "FreeBSD\n"},
			}},
			wantSub: "unsupported guest os \"FreeBSD\"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := detectGuestOS(tt.agent)
			if err == nil {
				t.Fatalf("detectGuestOS() = nil, want error containing %q", tt.wantSub)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("detectGuestOS() error = %q, want substring %q", err.Error(), tt.wantSub)
			}
		})
	}
}

func TestLaunchGuestTerminalRejectsEmptyCommand(t *testing.T) {
	err := launchGuestTerminal(&fakeGuestTerminalAgent{}, "user", nil)
	if err == nil || !strings.Contains(err.Error(), "terminal command is required") {
		t.Fatalf("launchGuestTerminal(empty) = %v, want 'terminal command is required'", err)
	}
}

func TestLaunchGuestTerminalUnsupportedOS(t *testing.T) {
	agent := &fakeGuestTerminalAgent{responses: []*controlpb.AgentExecResponse{
		{Stdout: "Darwin\n"},
	}}
	err := launchGuestTerminal(agent, "u", []string{"echo"})
	if err == nil || !strings.Contains(err.Error(), "not implemented for darwin") {
		t.Fatalf("launchGuestTerminal(darwin) = %v, want 'not implemented for darwin'", err)
	}
}

func TestLaunchGuestTerminalDetectError(t *testing.T) {
	agent := &erroringGuestTerminalAgent{err: errors.New("bad")}
	err := launchGuestTerminal(agent, "u", []string{"echo"})
	if err == nil || !strings.Contains(err.Error(), "detect guest os: bad") {
		t.Fatalf("launchGuestTerminal detect-err = %v", err)
	}
}

func TestLaunchLinuxGuestTerminalRejectsEmptyCommand(t *testing.T) {
	err := launchLinuxGuestTerminal(&fakeGuestTerminalAgent{}, "user", nil)
	if err == nil || !strings.Contains(err.Error(), "terminal command is required") {
		t.Fatalf("launchLinuxGuestTerminal(empty) = %v", err)
	}
}
