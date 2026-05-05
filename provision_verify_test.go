package main

import (
	"errors"
	"testing"

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
