package main

import (
	"strings"
	"testing"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

func TestRequireLinuxUserExists(t *testing.T) {
	tests := []struct {
		name     string
		resp     *controlpb.ControlResponse
		wantErr  string
		wantOK   bool
	}{
		{
			name:    "nil response",
			resp:    nil,
			wantErr: "check linux user: empty response",
		},
		{
			name:    "failure with error message",
			resp:    &controlpb.ControlResponse{Success: false, Error: "  vsock dial: refused  "},
			wantErr: "check linux user: vsock dial: refused",
		},
		{
			name:    "failure with empty error",
			resp:    &controlpb.ControlResponse{Success: false},
			wantErr: "check linux user: request failed",
		},
		{
			name: "agent exec exit 0",
			resp: &controlpb.ControlResponse{
				Success: true,
				Result: &controlpb.ControlResponse_AgentExecResult{
					AgentExecResult: &controlpb.AgentExecResponse{ExitCode: 0},
				},
			},
			wantOK: true,
		},
		{
			name: "agent exec nonzero exit",
			resp: &controlpb.ControlResponse{
				Success: true,
				Result: &controlpb.ControlResponse_AgentExecResult{
					AgentExecResult: &controlpb.AgentExecResponse{ExitCode: 1},
				},
			},
			wantErr: `user "alice" does not exist on this VM`,
		},
		{
			name:   "legacy data ok via empty data",
			resp:   &controlpb.ControlResponse{Success: true},
			wantOK: true,
		},
		{
			name:    "legacy data with nonzero exit",
			resp:    &controlpb.ControlResponse{Success: true, Data: `{"exitCode":2,"stderr":"no such user"}`},
			wantErr: `user "alice" does not exist on this VM`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := requireLinuxUserExists("alice", tt.resp)
			if tt.wantOK {
				if err != nil {
					t.Fatalf("requireLinuxUserExists = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("requireLinuxUserExists = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("requireLinuxUserExists err = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestLinuxUserMissingError(t *testing.T) {
	err := linuxUserMissingError("bob")
	if err == nil {
		t.Fatal("linuxUserMissingError returned nil")
	}
	msg := err.Error()
	for _, want := range []string{`"bob"`, "useradd -m bob", "cove ctl agent-exec --daemon"} {
		if !strings.Contains(msg, want) {
			t.Errorf("linuxUserMissingError = %q, missing %q", msg, want)
		}
	}
}
