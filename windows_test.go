package main

import (
	"path/filepath"
	"testing"

	"github.com/tmc/apple/foundation"
	"github.com/tmc/apple/objc"
	privvz "github.com/tmc/apple/private/virtualization"
	vz "github.com/tmc/apple/virtualization"
)

func TestParseWindowsGraphicsMode(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    windowsGraphics
		wantErr bool
	}{
		{name: "default", input: "", want: windowsGraphicsLinearFramebuffer},
		{name: "linear framebuffer", input: "linear-framebuffer", want: windowsGraphicsLinearFramebuffer},
		{name: "linear alias", input: "linear", want: windowsGraphicsLinearFramebuffer},
		{name: "virtio", input: "virtio", want: windowsGraphicsVirtio},
		{name: "bad", input: "ramfb", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseWindowsGraphicsMode(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseWindowsGraphicsMode(%q) error = nil", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseWindowsGraphicsMode(%q): %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("parseWindowsGraphicsMode(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestWindowsLinearFramebufferGraphicsDevice(t *testing.T) {
	if privvz.GetVZLinearFramebufferGraphicsDeviceConfigurationClass().Class() == 0 {
		t.Skip("_VZLinearFramebufferGraphicsDeviceConfiguration unavailable")
	}

	oldMode := windowsGraphicsMode
	oldDisplays := displays
	t.Cleanup(func() {
		windowsGraphicsMode = oldMode
		displays = oldDisplays
	})
	windowsGraphicsMode = string(windowsGraphicsLinearFramebuffer)
	displays = nil

	config, err := buildWindowsBaseConfigurationForTest(t)
	if err != nil {
		t.Fatal(err)
	}

	arrayID := objc.Send[objc.ID](config.ID, objc.Sel("graphicsDevices"))
	if arrayID == 0 {
		t.Fatal("graphicsDevices = nil")
	}
	graphics := foundation.NSArrayFromID(arrayID)
	if got := graphics.Count(); got != 1 {
		t.Fatalf("graphicsDevices count = %d, want 1", got)
	}
	if _, err := config.ValidateWithError(); err != nil {
		t.Fatalf("ValidateWithError: %v", err)
	}
}

func buildWindowsBaseConfigurationForTest(t *testing.T) (vz.VZVirtualMachineConfiguration, error) {
	t.Helper()

	oldVMDir := vmDir
	oldCPU := cpuCount
	oldMemory := memoryGB
	oldNetwork := networkMode
	oldSerial := serialOutput
	oldGDB := gdbAddress
	oldVNC := vncAddress
	oldVNCBonjour := vncBonjourService
	t.Cleanup(func() {
		vmDir = oldVMDir
		cpuCount = oldCPU
		memoryGB = oldMemory
		networkMode = oldNetwork
		serialOutput = oldSerial
		gdbAddress = oldGDB
		vncAddress = oldVNC
		vncBonjourService = oldVNCBonjour
	})

	vmDir = t.TempDir()
	cpuCount = 2
	memoryGB = 4
	networkMode = "none"
	serialOutput = "none"
	gdbAddress = ""
	vncAddress = ""
	vncBonjourService = ""

	disk := filepath.Join(vmDir, "windows-disk.img")
	if err := createDiskImage(disk, 1); err != nil {
		t.Fatalf("createDiskImage: %v", err)
	}
	return buildWindowsVMConfiguration(disk)
}
