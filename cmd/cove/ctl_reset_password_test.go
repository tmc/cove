package main

import (
	"strings"
	"testing"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestAutoLoginRefreshCommand(t *testing.T) {
	cmd, err := autoLoginRefreshCommand("testuser", "secret123")
	if err != nil {
		t.Fatalf("autoLoginRefreshCommand: %v", err)
	}
	if strings.Contains(cmd, "secret123") {
		t.Fatal("refresh command leaked the raw password")
	}
	for _, want := range []string{
		"base64 -D > /etc/kcpassword",
		"base64 -D > /Library/Preferences/com.apple.loginwindow.plist",
		"chown root:wheel /etc/kcpassword",
		"chown root:wheel /Library/Preferences/com.apple.loginwindow.plist",
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("refresh command missing %q", want)
		}
	}
}

func TestRequireAgentExecSuccess(t *testing.T) {
	tests := []struct {
		name   string
		action string
		resp   *controlpb.ControlResponse
		want   string
	}{
		{
			name:   "agent exec success",
			action: "reset password",
			resp: &controlpb.ControlResponse{
				Success: true,
				Result:  &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{ExitCode: 0}},
			},
		},
		{
			name:   "agent exec exit code",
			action: "reset password",
			resp: &controlpb.ControlResponse{
				Success: true,
				Result: &controlpb.ControlResponse_AgentExecResult{AgentExecResult: &controlpb.AgentExecResponse{
					ExitCode: 10,
					Stderr:   "eDSAuthFailed\nsecond line",
				}},
			},
			want: "reset password: eDSAuthFailed",
		},
		{
			name:   "legacy data exit code",
			action: "refresh autologin artifacts",
			resp: &controlpb.ControlResponse{
				Success: true,
				Data:    `{"exitCode":7,"stdout":"","stderr":"permission denied\n"}`,
			},
			want: "refresh autologin artifacts: permission denied",
		},
		{
			name:   "response error",
			action: "reset password",
			resp: &controlpb.ControlResponse{
				Success: false,
				Error:   "agent unavailable",
			},
			want: "reset password: agent unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := requireAgentExecSuccess(tt.action, tt.resp)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("requireAgentExecSuccess: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("requireAgentExecSuccess returned nil error")
			}
			if got := err.Error(); got != tt.want {
				t.Fatalf("error = %q, want %q", got, tt.want)
			}
		})
	}
}
