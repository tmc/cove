package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

func TestReadWindowsQEMUCTLStatus(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "windows.qcow2"), []byte("disk"), 0644); err != nil {
		t.Fatal(err)
	}
	qemuDir := filepath.Join(dir, "qemu")
	if err := os.Mkdir(qemuDir, 0755); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Date(2026, 5, 19, 1, 2, 3, 0, time.UTC)
	process := windowsQEMUProcessMetadata{
		State:           "running",
		CovePID:         123,
		QEMUPID:         456,
		StartedAt:       startedAt,
		MonitorSockPath: filepath.Join(qemuDir, "monitor.sock"),
	}
	data, err := json.Marshal(process)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(qemuDir, "process.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	got := readWindowsQEMUCTLStatus(dir)
	if got.Backend != "qemu-hvf" || got.State != "running" || got.QEMUPID != 456 {
		t.Fatalf("status = %#v", got)
	}
	if got.MonitorSockPath != filepath.Join(qemuDir, "monitor.sock") {
		t.Fatalf("monitor = %q", got.MonitorSockPath)
	}
}

func TestCtlCommandWindowsQEMUStatusBypassesControlSocket(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldVMName, oldVMDir := vmName, vmDir
	t.Cleanup(func() {
		vmName, vmDir = oldVMName, oldVMDir
	})
	vmName, vmDir = "", ""
	name := "qemu-win"
	dir := filepath.Join(vmconfig.BaseDir(), name+".covevm")
	if err := os.MkdirAll(filepath.Join(dir, "qemu"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "windows.qcow2"), []byte("disk"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "qemu", "process.json"), []byte(`{"state":"stopped","qemuPid":987}`), 0644); err != nil {
		t.Fatal(err)
	}

	out := captureCtlQEMUStdout(t, func() error {
		return ctlCommand([]string{"-vm", name, "status"})
	})
	for _, want := range []string{"state:   stopped", "backend: qemu-hvf", "qemuPid: 987"} {
		if !strings.Contains(out, want) {
			t.Fatalf("ctl qemu status output missing %q:\n%s", want, out)
		}
	}
}

func TestCtlCommandWindowsQEMUUnsupportedBypassesControlSocket(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldVMName, oldVMDir := vmName, vmDir
	t.Cleanup(func() {
		vmName, vmDir = oldVMName, oldVMDir
	})
	vmName, vmDir = "", ""
	name := "qemu-win"
	dir := filepath.Join(vmconfig.BaseDir(), name+".covevm")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "windows.qcow2"), []byte("disk"), 0644); err != nil {
		t.Fatal(err)
	}

	err := ctlCommand([]string{"-vm", name, "screenshot"})
	if err == nil {
		t.Fatal("ctlCommand succeeded, want unsupported qemu command")
	}
	if !strings.Contains(err.Error(), "not supported for qemu windows VMs") {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "control socket") {
		t.Fatalf("error fell through to control socket: %v", err)
	}
}

func captureCtlQEMUStdout(t *testing.T, fn func() error) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	err = fn()
	w.Close()
	os.Stdout = old
	if err != nil {
		t.Fatalf("function returned error: %v", err)
	}
	data, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
