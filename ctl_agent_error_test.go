package main

import (
	"strings"
	"testing"
)

func TestCtlAgentErrorLooksGuestFailure(t *testing.T) {
	tests := []struct {
		name   string
		detail string
		want   bool
	}{
		{"not found code", "not_found: stat /tmp/missing: no such file or directory", true},
		{"wrapped not found", "read: not_found: stat /tmp/missing: no such file or directory", true},
		{"permission code", "permission_denied: open /root/x: permission denied", true},
		{"exit status", "exec: exit status 2", true},
		{"connect unavailable", "connect vsock port 1024: connection refused", false},
		{"agent missing", "agent not ready", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ctlAgentErrorLooksGuestFailure(tt.detail); got != tt.want {
				t.Fatalf("ctlAgentErrorLooksGuestFailure(%q) = %v, want %v", tt.detail, got, tt.want)
			}
		})
	}
}

func TestCtlAgentCommandErrorGuestFailureDoesNotSayUnavailable(t *testing.T) {
	err := ctlAgentCommandError("/tmp/no-such.sock", "agent-read", "not_found: stat /tmp/missing: no such file or directory")
	if err == nil {
		t.Fatal("ctlAgentCommandError = nil, want error")
	}
	msg := err.Error()
	if strings.Contains(msg, "guest agent unavailable") {
		t.Fatalf("error = %q, should not classify missing file as agent unavailable", msg)
	}
	for _, want := range []string{"agent-read failed", "not_found", "/tmp/missing"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error = %q, want substring %q", msg, want)
		}
	}
}
