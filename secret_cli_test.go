package main

import (
	"strings"
	"testing"
)

func TestHandleSecretProbePrintsLengthOnly(t *testing.T) {
	t.Setenv("COVE_SECRET_PROBE", "super-secret")
	out := captureStdout(t, func() error {
		return handleSecretCommand([]string{"probe", "env://COVE_SECRET_PROBE"})
	})
	if !strings.Contains(out, "secret resolved: env://COVE_SECRET_PROBE (length: 12 bytes)") {
		t.Fatalf("output = %q", out)
	}
	if strings.Contains(out, "super-secret") {
		t.Fatalf("output leaked secret value: %q", out)
	}
}

func TestHandleSecretProbeErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing uri", args: []string{"probe"}, want: "secret probe requires: <uri>"},
		{name: "unsupported scheme", args: []string{"probe", "vault://secret"}, want: "unsupported secret URI scheme"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := handleSecretCommand(tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("handleSecretCommand(%v) = %v, want %q", tt.args, err, tt.want)
			}
		})
	}
}
