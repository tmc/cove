package main

import (
	"errors"
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
		if !errors.Is(err, ErrInjectFlagRequired) {
			t.Fatalf("handleProvision missing -user: got %v, want ErrInjectFlagRequired", err)
		}
	})

	t.Run("apple app sandbox denies provisioning", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv(appleAppSandboxContainerEnv, "com.tmc.cove")
		oldVMDir, oldVMName := vmDir, vmName
		t.Cleanup(func() { vmDir, vmName = oldVMDir, oldVMName })
		vmDir = t.TempDir()
		vmName = "sandboxed-provision"
		_, err := captureStdoutResult(t, func() error {
			return handleProvision([]string{"-stage-only"})
		})
		if !errors.Is(err, errAppleAppSandboxHostAccessDenied) {
			t.Fatalf("handleProvision sandbox error = %v, want Apple App Sandbox denial", err)
		}
	})
}
