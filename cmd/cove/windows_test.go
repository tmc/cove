package main

import (
	"path/filepath"
	"strings"
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
		{name: "default", input: "", want: windowsGraphicsVirtio},
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

func TestParseWindowsSerialMode(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    windowsSerial
		wantErr bool
	}{
		{name: "default", input: "", want: windowsSerialVirtio},
		{name: "virtio", input: "virtio", want: windowsSerialVirtio},
		{name: "pl011", input: "pl011", want: windowsSerialPL011},
		{name: "16550", input: "16550", want: windowsSerial16550},
		{name: "bad", input: "com1", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseWindowsSerialMode(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseWindowsSerialMode(%q) error = nil", tt.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseWindowsSerialMode(%q): %v", tt.input, err)
			}
			if got != tt.want {
				t.Fatalf("parseWindowsSerialMode(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestWindowsISOLabel(t *testing.T) {
	got := windowsISOLabel("/tmp/26100.4349.250607-1500.ge_release_svc_refresh_CLIENTCONSUMER_RET_A64FRE_en-us.esd")
	const want = "26100.4349.250607-1500"
	if got != want {
		t.Fatalf("windowsISOLabel() = %q, want %q", got, want)
	}
}

func TestLooksWindowsISOName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{name: "windows.iso", want: true},
		{name: "Win11_ARM64.iso", want: true},
		{name: "26100.4349_CLIENTCONSUMER_RET_A64FRE_en-us.iso", want: true},
		{name: "darwin.iso", want: false},
		{name: "ubuntu.iso", want: false},
	}
	for _, tt := range tests {
		if got := looksWindowsISOName(tt.name); got != tt.want {
			t.Fatalf("looksWindowsISOName(%q) = %v, want %v", tt.name, got, tt.want)
		}
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
		if strings.Contains(err.Error(), "Virtualization is not available") {
			t.Skipf("Virtualization.framework unavailable: %v", err)
		}
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
	oldWindowsSerial := windowsSerialMode
	oldGDB := gdbAddress
	oldVNC := vncAddress
	oldVNCBonjour := vncBonjourService
	t.Cleanup(func() {
		vmDir = oldVMDir
		cpuCount = oldCPU
		memoryGB = oldMemory
		networkMode = oldNetwork
		serialOutput = oldSerial
		windowsSerialMode = oldWindowsSerial
		gdbAddress = oldGDB
		vncAddress = oldVNC
		vncBonjourService = oldVNCBonjour
	})

	vmDir = t.TempDir()
	cpuCount = 2
	memoryGB = 4
	networkMode = "none"
	serialOutput = "none"
	windowsSerialMode = string(windowsSerialVirtio)
	gdbAddress = ""
	vncAddress = ""
	vncBonjourService = ""

	disk := filepath.Join(vmDir, "windows-disk.img")
	if err := createDiskImage(disk, 1); err != nil {
		t.Fatalf("createDiskImage: %v", err)
	}
	return buildWindowsVMConfiguration(disk)
}
