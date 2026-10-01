package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/cove/internal/mutationguard"
)

func TestRuntimeAdmissionHandsOffRootGuard(t *testing.T) {
	pinsTestHome(t)
	dir := filepath.Join(coveRoot(), "vms", "runtime.covevm")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	guard, err := mutationguard.Acquire(coveRoot())
	if err != nil {
		t.Fatal(err)
	}
	if lock, err := acquireRuntimeRunLock(dir, defaultRunHooks()); !errors.Is(err, mutationguard.ErrBusy) || lock != nil {
		t.Fatalf("runtime admitted during mutation: %v", err)
	}
	guard.Release()
	lock, err := acquireRuntimeRunLock(dir, defaultRunHooks())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	guard, err = mutationguard.Acquire(coveRoot())
	if err != nil {
		t.Fatalf("runtime retained root guard: %v", err)
	}
	defer guard.Release()
	if competing, err := AcquireRunLock(dir); !errors.Is(err, ErrRunLockHeld) || competing != nil {
		t.Fatalf("runtime released VM lock: %v", err)
	}
}

func TestVMMutationsRespectRootGuard(t *testing.T) {
	pinsTestHome(t)
	guard, err := mutationguard.Acquire(coveRoot())
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Release()
	tests := []struct {
		name string
		run  func() error
	}{
		{"delete", func() error { return DeleteVMWithOptions("missing", DeleteVMOptions{Cascade: true}) }},
		{"rename", func() error { return RenameVM("missing", "target") }},
		{"import", func() error { return ImportVM("missing.tar.gz", "target") }},
		{"pin", func() error { return handlePinCommand(commandTestEnv(), []string{"vm:target"}) }},
		{"unpin", func() error { return handleUnpinCommand(commandTestEnv(), []string{"vm:target"}) }},
		{"template-save", func() error {
			return SaveTemplateWithOptions(SaveTemplateOptions{VMName: "missing", TemplateName: "saved"})
		}},
		{"template-create", func() error {
			return CreateFromTemplateWithOptions(CreateFromTemplateOptions{TemplateName: "missing", VMName: "target"})
		}},
		{"template-delete", func() error { return DeleteTemplate("missing") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); !errors.Is(err, mutationguard.ErrBusy) {
				t.Fatalf("operation bypassed root guard: %v", err)
			}
		})
	}
}

func TestForkMutationsRespectRootGuard(t *testing.T) {
	pinsTestHome(t)
	guard, err := mutationguard.Acquire(coveRoot())
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Release()
	tests := []struct {
		name string
		run  func() error
	}{
		{"clone", func() error { return CloneVM(CloneOptions{Source: "missing", Target: "target"}) }},
		{"fork", func() error { return ForkVM("missing", "target") }},
		{"snapshot", func() error {
			return ForkVMWithSnapshot(ForkVMOptions{Parent: "missing", Child: "target", Snapshot: "saved"})
		}},
		{"ephemeral", func() error { _, err := SetupEphemeralFork(EphemeralForkOptions{Parent: "missing"}); return err }},
		{"ephemeral-cleanup", func() error { return CleanupEphemeralFork("missing") }},
		{"ephemeral-gc", func() error { _, err := GCEphemeralForks(EphemeralGCOptions{}); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); !errors.Is(err, mutationguard.ErrBusy) {
				t.Fatalf("mutation bypassed root guard: %v", err)
			}
		})
	}
}
