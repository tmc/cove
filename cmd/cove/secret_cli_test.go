package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestHandleSecretProbePrintsLengthOnly(t *testing.T) {
	t.Setenv("COVE_SECRET_PROBE", "super-secret")
	var out bytes.Buffer
	env := commandTestEnv()
	env.Stdout = &out
	if err := handleSecretCommand(env, []string{"probe", "env://COVE_SECRET_PROBE"}); err != nil {
		t.Fatalf("handleSecretCommand: %v", err)
	}
	if !strings.Contains(out.String(), "secret resolved: env://COVE_SECRET_PROBE (length: 12 bytes)") {
		t.Fatalf("output = %q", out.String())
	}
	if strings.Contains(out.String(), "super-secret") {
		t.Fatalf("output leaked secret value: %q", out.String())
	}
}

func TestHandleSecretProbeHelp(t *testing.T) {
	var stderr bytes.Buffer
	env := commandTestEnv()
	env.Stderr = &stderr
	if err := handleSecretCommand(env, []string{"probe", "-h"}); err != nil {
		t.Fatalf("handleSecretCommand(probe -h): %v", err)
	}
	for _, want := range []string{"Usage: cove secret probe <uri>", "secret value is never", "env://VAR_NAME"} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr.String())
		}
	}
}

func TestHandleSecretProbeErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing uri", args: []string{"probe"}, want: "usage: cove secret probe <uri>"},
		{name: "unsupported scheme", args: []string{"probe", "vault://secret"}, want: "unsupported secret URI scheme"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := handleSecretCommand(commandTestEnv(), tt.args)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("handleSecretCommand(%v) = %v, want %q", tt.args, err, tt.want)
			}
		})
	}
}
