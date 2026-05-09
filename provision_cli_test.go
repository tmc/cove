package main

import (
	"strings"
	"testing"
)

// TestHandleProvisionEarlyReturns covers handleProvision flag-parse and
// missing-flag branches that don't touch the disk-mount/agent-build paths.
func TestHandleProvisionEarlyReturns(t *testing.T) {
	t.Run("help flag returns nil", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		oldVMDir, oldVMName := vmDir, vmName
		t.Cleanup(func() { vmDir, vmName = oldVMDir, oldVMName })
		vmDir = t.TempDir()
		vmName = "h"
		_, err := captureStdoutResult(t, func() error {
			return handleProvision([]string{"-h"})
		})
		if err != nil {
			t.Fatalf("handleProvision -h: %v, want nil", err)
		}
	})

	t.Run("unknown flag returns parse error", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		oldVMDir, oldVMName := vmDir, vmName
		t.Cleanup(func() { vmDir, vmName = oldVMDir, oldVMName })
		vmDir = t.TempDir()
		vmName = "h"
		_, err := captureStdoutResult(t, func() error {
			return handleProvision([]string{"-not-a-real-flag"})
		})
		if err == nil {
			t.Fatal("handleProvision unknown flag: got nil, want parse error")
		}
	})

	t.Run("missing user returns required-flag error", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		oldVMDir, oldVMName := vmDir, vmName
		t.Cleanup(func() { vmDir, vmName = oldVMDir, oldVMName })
		vmDir = t.TempDir()
		vmName = "missing-user"
		_, err := captureStdoutResult(t, func() error {
			return handleProvision([]string{"-stage-only"})
		})
		if err == nil || !strings.Contains(err.Error(), "missing required flag: -user") {
			t.Fatalf("handleProvision missing -user: got %v, want 'missing required flag: -user'", err)
		}
	})
}
