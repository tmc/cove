package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestQEMUDoctorSelectionInclude(t *testing.T) {
	tests := []struct {
		name string
		sel  qemuDoctorSelection
		want bool
	}{
		{name: "plain macos host", sel: qemuDoctorSelection{}},
		{name: "windows vm present", sel: qemuDoctorSelection{WindowsVMs: 1}, want: true},
		{name: "explicit windows invocation", sel: qemuDoctorSelection{Explicit: true}, want: true},
		{name: "both", sel: qemuDoctorSelection{Explicit: true, WindowsVMs: 2}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.sel.include(); got != tt.want {
				t.Fatalf("include() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCountWindowsVMs(t *testing.T) {
	tests := []struct {
		name string
		// vms maps a VM directory name to the marker file that gives it an OS.
		vms  map[string]string
		want int
	}{
		{name: "empty root", vms: map[string]string{}},
		{name: "macos only", vms: map[string]string{"mac": "hw.model"}},
		{name: "windows vz", vms: map[string]string{"win": "windows-disk.img"}, want: 1},
		{name: "windows qemu", vms: map[string]string{"winq": "windows.qcow2"}, want: 1},
		{name: "mixed", vms: map[string]string{"mac": "hw.model", "linux": "linux-disk.img", "winq": "windows.qcow2", "win": "windows-disk.img"}, want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for vm, marker := range tt.vms {
				dir := filepath.Join(root, vm)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, marker), []byte(marker), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if got := countWindowsVMs(root); got != tt.want {
				t.Fatalf("countWindowsVMs = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCountWindowsVMsMissingRoot(t *testing.T) {
	if got := countWindowsVMs(filepath.Join(t.TempDir(), "absent")); got != 0 {
		t.Fatalf("countWindowsVMs = %d, want 0", got)
	}
}

// TestHostDoctorSkipsQEMUChecks covers the selection wired into the default
// host report: a Mac with no Windows VM and no Windows intent must not be
// charged for the QEMU probes.
func TestHostDoctorSkipsQEMUChecks(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	// Point every QEMU probe at a path that cannot exist so the report is
	// assembled without executing a real qemu binary.
	t.Setenv("COVE_QEMU_SYSTEM_AARCH64", filepath.Join(dir, "absent-qemu"))
	t.Setenv("COVE_QEMU_IMG", filepath.Join(dir, "absent-qemu-img"))
	t.Cleanup(func() { windowsMode, windowsBackendMode = false, "vz" })

	session := qemuDoctorSessionName
	qemuDoctorSessionName = func() (string, error) { return "Aqua", nil }
	t.Cleanup(func() { qemuDoctorSessionName = session })

	windowsMode, windowsBackendMode = false, "vz"
	if hostDoctorHasQEMUCheck(collectHostDoctorReport()) {
		t.Fatal("host doctor ran the QEMU checks with no Windows VM and no Windows intent")
	}

	windowsMode = true
	if !hostDoctorHasQEMUCheck(collectHostDoctorReport()) {
		t.Fatal("host doctor skipped the QEMU checks for an explicit -windows invocation")
	}
}

func hostDoctorHasQEMUCheck(report hostDoctorReport) bool {
	for _, check := range report.Checks {
		if check.Name == "qemu/qemu-version" {
			return true
		}
	}
	return false
}
