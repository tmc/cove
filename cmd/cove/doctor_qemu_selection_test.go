package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQEMUDoctorSelectionInclude(t *testing.T) {
	tests := []struct {
		name string
		sel  qemuDoctorSelection
		want bool
	}{
		{name: "plain macos host", sel: qemuDoctorSelection{}},
		{name: "windows vm present", sel: qemuDoctorSelection{QEMUWindowsVMs: 1}, want: true},
		{name: "explicit windows invocation", sel: qemuDoctorSelection{Explicit: true}, want: true},
		{name: "both", sel: qemuDoctorSelection{Explicit: true, QEMUWindowsVMs: 2}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.sel.include(); got != tt.want {
				t.Fatalf("include() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCountQEMUWindowsVMs(t *testing.T) {
	tests := []struct {
		name string
		// vms maps a VM directory name to the marker file that gives it a
		// layout. A trailing slash makes the marker a directory.
		vms  map[string]string
		want int
	}{
		{name: "empty root", vms: map[string]string{}},
		{name: "macos only", vms: map[string]string{"mac": "hw.model"}},
		{name: "windows vz", vms: map[string]string{"win": "windows-disk.img"}},
		{name: "windows qemu disk", vms: map[string]string{"winq": "windows.qcow2"}, want: 1},
		{name: "windows qemu dir", vms: map[string]string{"winq": "qemu/"}, want: 1},
		{name: "mixed", vms: map[string]string{"mac": "hw.model", "linux": "linux-disk.img", "winq": "windows.qcow2", "win": "windows-disk.img"}, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for vm, marker := range tt.vms {
				dir := filepath.Join(root, vm)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if strings.HasSuffix(marker, "/") {
					if err := os.MkdirAll(filepath.Join(dir, marker), 0o755); err != nil {
						t.Fatal(err)
					}
					continue
				}
				if err := os.WriteFile(filepath.Join(dir, marker), []byte(marker), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if got := countQEMUWindowsVMs(root); got != tt.want {
				t.Fatalf("countQEMUWindowsVMs = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCountQEMUWindowsVMsMissingRoot(t *testing.T) {
	if got := countQEMUWindowsVMs(filepath.Join(t.TempDir(), "absent")); got != 0 {
		t.Fatalf("countQEMUWindowsVMs = %d, want 0", got)
	}
}

// TestHostDoctorSkipsQEMUChecks covers the selection wired into the default
// host report: a Mac with no QEMU-backed Windows VM and no QEMU Windows intent
// must not be charged for the QEMU probes, including a -windows invocation
// that asked for the Virtualization.framework backend.
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

	windowsMode, windowsBackendMode = false, "qemu"
	if hostDoctorHasQEMUCheck(collectHostDoctorReport()) {
		t.Fatal("host doctor ran the QEMU checks with no QEMU Windows VM and no Windows intent")
	}

	windowsMode, windowsBackendMode = true, "vz"
	if hostDoctorHasQEMUCheck(collectHostDoctorReport()) {
		t.Fatal("host doctor ran the QEMU checks for a vz-backend Windows invocation")
	}

	windowsMode, windowsBackendMode = true, "qemu"
	if !hostDoctorHasQEMUCheck(collectHostDoctorReport()) {
		t.Fatal("host doctor skipped the QEMU checks for an explicit -windows-backend qemu invocation")
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
