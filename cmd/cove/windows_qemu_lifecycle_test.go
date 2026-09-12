package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/cove/internal/vmrun"
)

func TestWindowsQEMUEarlyExit(t *testing.T) {
	for _, code := range []string{"0", "1"} {
		t.Run(code, func(t *testing.T) {
			dir := t.TempDir()
			executable := filepath.Join(dir, "qemu")
			if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit "+code+"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			cfg := windowsQEMUConfig{QEMUPath: executable, VMDir: dir, MonitorSockPath: filepath.Join(dir, "monitor.sock"), CPUCount: 2, MemoryGB: 4, NetworkMode: "none", Headless: true}
			result := make(chan error, 1)
			go func() { result <- runWindowsQEMU(cfg, false) }()
			select {
			case err := <-result:
				if err == nil || !strings.Contains(err.Error(), "exited before monitor was ready") {
					t.Fatalf("runWindowsQEMU = %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("runWindowsQEMU hung after child exited")
			}
		})
	}
}

func TestWindowsInstallPreservesExistingDisk(t *testing.T) {
	for _, kind := range []string{"file", "directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			disk := filepath.Join(dir, "windows.qcow2")
			switch kind {
			case "file":
				if err := os.WriteFile(disk, []byte("existing guest data"), 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(disk, 0700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(filepath.Join(dir, "absent"), disk); err != nil {
					t.Fatal(err)
				}
			}
			err := installWindowsQEMUVMWithConfig(vmrun.RunConfig{}, vmrun.HostConfig{VMDir: dir}, io.Discard)
			if err == nil || !strings.Contains(err.Error(), "existing disk") {
				t.Fatalf("install = %v", err)
			}
			if kind == "file" {
				got, err := os.ReadFile(disk)
				if err != nil || string(got) != "existing guest data" {
					t.Fatalf("disk = %q, %v", got, err)
				}
			}
			if _, err := os.Stat(filepath.Join(dir, "qemu")); !os.IsNotExist(err) {
				t.Fatalf("installer created qemu directory: %v", err)
			}
		})
	}
}

func TestWindowsQEMUResolvedDiskPath(t *testing.T) {
	for _, tt := range []struct{ name, input, want string }{
		{"default relative directory", "", filepath.Join("vm", "windows.qcow2")},
		{"custom relative path", "disk.qcow2", filepath.Join("vm", "disk.qcow2")},
		{"absolute path", "/tmp/disk.qcow2", "/tmp/disk.qcow2"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := windowsQEMUResolvedDiskPath(tt.input, "vm"); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWindowsVZInstallPreservesExistingDisk(t *testing.T) {
	restoreVMGlobals(t)
	oldPath, oldExplicit := diskPath, windowsBackendExplicit
	t.Cleanup(func() { diskPath, windowsBackendExplicit = oldPath, oldExplicit })
	vmDir = t.TempDir()
	diskPath = ""
	windowsBackendMode, windowsBackendExplicit = "vz", true
	cpuCount, memoryGB = 2, 4
	path := filepath.Join(vmDir, "windows-disk.img")
	if err := os.WriteFile(path, []byte("existing guest data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := installWindowsVM(io.Discard); err == nil || !strings.Contains(err.Error(), "existing disk") {
		t.Fatalf("installWindowsVM = %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "existing guest data" {
		t.Fatalf("disk = %q, %v", got, err)
	}
	lock, err := AcquireRunLock(vmDir)
	if err != nil {
		t.Fatalf("install left run lock held: %v", err)
	}
	defer lock.Release()
	if err := installWindowsVM(io.Discard); err == nil || !strings.Contains(err.Error(), "run.lock") {
		t.Fatalf("installWindowsVM with held lock = %v", err)
	}
}
