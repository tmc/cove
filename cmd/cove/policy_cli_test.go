package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/vmconfig"
)

func TestHandlePolicyCommandSetShowClear(t *testing.T) {
	withTempHome(t)
	vmName := "policy-vm"
	var out bytes.Buffer
	env := commandEnv{Stdout: &out, Stderr: new(bytes.Buffer)}

	if err := handlePolicyCommand(env, []string{vmName, "idle", "30m"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if !strings.Contains(out.String(), "Saved policy for "+vmName) {
		t.Fatalf("set output = %q", out.String())
	}
	out.Reset()
	if err := handlePolicyCommand(env, []string{vmName, "show"}); err != nil {
		t.Fatalf("show: %v", err)
	}
	if !strings.Contains(out.String(), "Idle timeout: 30m0s") {
		t.Fatalf("show output = %q", out.String())
	}
	if !strings.Contains(out.String(), "Run budget:   -") {
		t.Fatalf("show output = %q", out.String())
	}
	out.Reset()
	if err := handlePolicyCommand(env, []string{vmName, "clear"}); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if _, err := os.Stat(filepath.Join(vmconfig.Path(vmName), "policy.json")); !os.IsNotExist(err) {
		t.Fatalf("policy.json exists after clear: %v", err)
	}
}

func TestHandlePolicyCommandRejectsBadInput(t *testing.T) {
	withTempHome(t)
	tests := []struct {
		name string
		args []string
	}{
		{name: "unknown field", args: []string{"vm", "bogus", "1"}},
		{name: "bad duration", args: []string{"vm", "idle", "nope"}},
		{name: "bad budget", args: []string{"vm", "run-budget", "zero"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := handlePolicyCommand(commandEnv{Stdout: new(bytes.Buffer), Stderr: new(bytes.Buffer)}, tt.args); err == nil {
				t.Fatalf("handlePolicyCommand(%v) = nil, want error", tt.args)
			}
		})
	}
}
