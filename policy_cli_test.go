package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

func TestHandlePolicyCommandSetShowClear(t *testing.T) {
	withTempHome(t)
	vmName := "policy-vm"

	out := captureStdout(t, func() error {
		return handlePolicyCommand([]string{vmName, "idle", "30m"})
	})
	if !strings.Contains(out, "Saved policy for "+vmName) {
		t.Fatalf("set output = %q", out)
	}
	out = captureStdout(t, func() error {
		return handlePolicyCommand([]string{vmName, "show"})
	})
	if !strings.Contains(out, "Idle timeout: 30m0s") {
		t.Fatalf("show output = %q", out)
	}
	if !strings.Contains(out, "Run budget:   -") {
		t.Fatalf("show output = %q", out)
	}
	if err := handlePolicyCommand([]string{vmName, "clear"}); err != nil {
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
			if err := handlePolicyCommand(tt.args); err == nil {
				t.Fatalf("handlePolicyCommand(%v) = nil, want error", tt.args)
			}
		})
	}
}
