package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/cove/internal/mutationguard"
	"github.com/tmc/cove/internal/vmconfig"
	"github.com/tmc/cove/internal/vmrun"
)

func TestInstallerGuardRunLockHandoff(t *testing.T) {
	t.Setenv(vmconfig.StateDirEnv, t.TempDir())
	path := vmconfig.Path("install")
	lock, err := acquireInstallerRunLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	guard, err := mutationguard.Acquire(coveRoot())
	if err != nil {
		t.Fatalf("root guard retained during installation: %v", err)
	}
	guard.Release()
	other, err := AcquireRunLock(path)
	if other != nil {
		other.Release()
	}
	if !errors.Is(err, ErrRunLockHeld) {
		t.Fatalf("run lock error = %v, want held", err)
	}
	if err := CleanupDisposableClone(path); !errors.Is(err, ErrRunLockHeld) {
		t.Fatalf("cleanup error = %v, want held", err)
	}
}

func TestInstallerEntrypointsGuardBeforeMutation(t *testing.T) {
	t.Setenv(vmconfig.StateDirEnv, t.TempDir())
	path := vmconfig.Path("install")
	oldVMDir := vmDir
	oldBackend, oldExplicit := windowsBackendMode, windowsBackendExplicit
	oldCPU, oldMemory := cpuCount, memoryGB
	vmDir = path
	windowsBackendMode, windowsBackendExplicit = "vz", true
	cpuCount, memoryGB = 2, 4
	t.Cleanup(func() {
		vmDir = oldVMDir
		windowsBackendMode, windowsBackendExplicit = oldBackend, oldExplicit
		cpuCount, memoryGB = oldCPU, oldMemory
	})
	guard, err := mutationguard.Acquire(coveRoot())
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Release()
	tests := []struct {
		name string
		run  func() error
	}{
		{"macOS", func() error {
			return installMacOSLikeVZWithProvision(context.Background(), io.Discard, macOSInstallProvision{}, "", vmrun.RunConfig{OS: vmrun.GuestMacOS}, vmrun.HostConfig{VMDir: path})
		}},
		{"linux", func() error { return installLinuxVMWithConfig(io.Discard, DefaultLinuxProvisionConfig()) }},
		{"windows qemu", func() error {
			return installWindowsQEMUVMWithConfig(vmrun.RunConfig{}, vmrun.HostConfig{VMDir: path}, io.Discard)
		}},
		{"windows vz", func() error { return installWindowsVM(io.Discard) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); !errors.Is(err, mutationguard.ErrBusy) {
				t.Fatalf("install error = %v, want root guard held", err)
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("installer created bundle before guarding: %v", err)
			}
		})
	}
}

func TestInstallerForceRefusesRunningBundle(t *testing.T) {
	t.Setenv(vmconfig.StateDirEnv, t.TempDir())
	path := vmconfig.Path("running")
	lock, err := acquireInstallerRunLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	disk := filepath.Join(path, "disk.img")
	if err := os.WriteFile(disk, []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	err = installMacOSLikeVZWithProvision(context.Background(), io.Discard, macOSInstallProvision{}, "", vmrun.RunConfig{OS: vmrun.GuestMacOS, ForceInstall: true}, vmrun.HostConfig{VMDir: path})
	if !errors.Is(err, ErrRunLockHeld) {
		t.Fatalf("install error = %v, want run lock held", err)
	}
	data, err := os.ReadFile(disk)
	if err != nil || string(data) != "retained" {
		t.Fatalf("running disk changed: %q, %v", data, err)
	}
}

func TestQEMURunDoesNotRecreateMissingBundle(t *testing.T) {
	t.Setenv(vmconfig.StateDirEnv, t.TempDir())
	path := vmconfig.Path("missing")
	if err := runWindowsQEMUVMWithConfig(vmrun.RunConfig{}, vmrun.HostConfig{VMDir: path}); err == nil {
		t.Fatal("running missing bundle succeeded")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("runner recreated missing bundle: %v", err)
	}
	if err := ensureWindowsQEMUEFIVars(filepath.Join(path, "qemu", "vars"), "missing-template"); err == nil {
		t.Fatal("creating vars in missing bundle succeeded")
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("vars helper recreated missing bundle: %v", err)
	}
}
