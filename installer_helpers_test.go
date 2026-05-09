package main

import (
	"os"
	"path/filepath"
	"testing"

	vz "github.com/tmc/apple/virtualization"
)

func TestInstallerWindowTitleForVM(t *testing.T) {
	tests := []struct {
		name string
		sel  vmSelection
		want string
	}{
		{"empty", vmSelection{}, "macOS VM Installation"},
		{"named", vmSelection{Name: "work"}, "macOS VM Installation — work"},
		{"named with dir", vmSelection{Name: "dev", Directory: "/tmp/x"}, "macOS VM Installation — dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := installerWindowTitleForVM(tt.sel); got != tt.want {
				t.Fatalf("installerWindowTitleForVM(%+v) = %q, want %q", tt.sel, got, tt.want)
			}
		})
	}
}

func TestVMStateName(t *testing.T) {
	tests := []struct {
		state vz.VZVirtualMachineState
		want  string
	}{
		{vz.VZVirtualMachineStateStopped, "Stopped"},
		{vz.VZVirtualMachineStateRunning, "Running"},
		{vz.VZVirtualMachineStatePaused, "Paused"},
		{vz.VZVirtualMachineStateError, "Error"},
		{vz.VZVirtualMachineStateStarting, "Starting"},
		{vz.VZVirtualMachineStatePausing, "Pausing"},
		{vz.VZVirtualMachineStateResuming, "Resuming"},
		{vz.VZVirtualMachineStateStopping, "Stopping"},
		{vz.VZVirtualMachineStateSaving, "Saving"},
		{vz.VZVirtualMachineStateRestoring, "Restoring"},
		{vz.VZVirtualMachineState(9999), "Unknown(9999)"},
	}
	for _, tt := range tests {
		if got := vmStateName(tt.state); got != tt.want {
			t.Errorf("vmStateName(%d) = %q, want %q", tt.state, got, tt.want)
		}
	}
}

func TestIPSWLooksComplete(t *testing.T) {
	dir := t.TempDir()

	missing := filepath.Join(dir, "missing.ipsw")
	if ipswLooksComplete(missing) {
		t.Errorf("missing file: got true, want false")
	}

	tooSmall := filepath.Join(dir, "small.ipsw")
	if err := os.WriteFile(tooSmall, []byte("not really an ipsw"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ipswLooksComplete(tooSmall) {
		t.Errorf("under-1GB file: got true, want false")
	}

	// Build a >=1 GB file with a sparse hole, then append a valid EOCD signature
	// at the tail so the heuristic accepts it as a complete zip/ipsw.
	complete := filepath.Join(dir, "complete.ipsw")
	f, err := os.Create(complete)
	if err != nil {
		t.Fatal(err)
	}
	const minSize = int64(1*1024*1024*1024) + 4096
	if err := f.Truncate(minSize); err != nil {
		f.Close()
		t.Fatal(err)
	}
	// EOCD record: signature + zeros for the rest of the 22-byte minimum.
	eocd := []byte{0x50, 0x4b, 0x05, 0x06, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	if _, err := f.WriteAt(eocd, minSize-int64(len(eocd))); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if !ipswLooksComplete(complete) {
		t.Errorf("valid >=1GB file with EOCD: got false, want true")
	}

	// Same size but no EOCD signature anywhere in the tail.
	noEOCD := filepath.Join(dir, "no-eocd.ipsw")
	f2, err := os.Create(noEOCD)
	if err != nil {
		t.Fatal(err)
	}
	if err := f2.Truncate(minSize); err != nil {
		f2.Close()
		t.Fatal(err)
	}
	if err := f2.Close(); err != nil {
		t.Fatal(err)
	}
	if ipswLooksComplete(noEOCD) {
		t.Errorf(">=1GB file without EOCD: got true, want false")
	}
}
