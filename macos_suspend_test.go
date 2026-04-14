package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCurrentUSBControllerFingerprintCount(t *testing.T) {
	oldRuntimeProfile := runtimeProfile
	t.Cleanup(func() {
		runtimeProfile = oldRuntimeProfile
	})

	tests := []struct {
		name           string
		runtimeProfile string
		want           int
	}{
		{name: "full", runtimeProfile: "full", want: 1},
		{name: "minimal", runtimeProfile: "minimal", want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtimeProfile = tt.runtimeProfile
			if got := currentUSBControllerFingerprintCount(); got != tt.want {
				t.Fatalf("currentUSBControllerFingerprintCount() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCurrentVirtioDeviceFingerprintCounts(t *testing.T) {
	oldVMDir := vmDir
	oldRuntimeProfile := runtimeProfile
	oldVolumes := volumes
	oldShareDir := shareDir
	t.Cleanup(func() {
		vmDir = oldVMDir
		runtimeProfile = oldRuntimeProfile
		volumes = oldVolumes
		shareDir = oldShareDir
	})

	vmDir = t.TempDir()
	volumes = nil
	shareDir = ""

	tests := []struct {
		name           string
		runtimeProfile string
		volumes        volumeSlice
		wantDirSharing int
		wantSockets    int
		wantBalloon    int
	}{
		{name: "full default", runtimeProfile: "full", wantDirSharing: 1, wantSockets: 1, wantBalloon: 1},
		{
			name:           "full with tagged volumes",
			runtimeProfile: "full",
			volumes: volumeSlice{
				{HostPath: "/tmp/a", Tag: "a"},
				{HostPath: "/tmp/b", Tag: "b"},
			},
			wantDirSharing: 3,
			wantSockets:    1,
			wantBalloon:    1,
		},
		{name: "minimal", runtimeProfile: "minimal", wantDirSharing: 0, wantSockets: 0, wantBalloon: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtimeProfile = tt.runtimeProfile
			volumes = tt.volumes
			if got := currentDirectorySharingDeviceFingerprintCount(); got != tt.wantDirSharing {
				t.Fatalf("currentDirectorySharingDeviceFingerprintCount() = %d, want %d", got, tt.wantDirSharing)
			}
			if got := currentSocketDeviceFingerprintCount(); got != tt.wantSockets {
				t.Fatalf("currentSocketDeviceFingerprintCount() = %d, want %d", got, tt.wantSockets)
			}
			if got := currentBalloonDeviceFingerprintCount(); got != tt.wantBalloon {
				t.Fatalf("currentBalloonDeviceFingerprintCount() = %d, want %d", got, tt.wantBalloon)
			}
		})
	}
}

func TestCurrentConfigFingerprintIncludesVirtioRuntimeSurface(t *testing.T) {
	oldVMDir := vmDir
	oldRuntimeProfile := runtimeProfile
	oldCPUCount := cpuCount
	oldMemoryGB := memoryGB
	oldNetworkMode := networkMode
	oldDisplays := displays
	oldVolumes := volumes
	oldShareDir := shareDir
	oldUSBDevices := usbDevices
	oldEnableClipboard := enableClipboard
	oldSerialOutput := serialOutput
	t.Cleanup(func() {
		vmDir = oldVMDir
		runtimeProfile = oldRuntimeProfile
		cpuCount = oldCPUCount
		memoryGB = oldMemoryGB
		networkMode = oldNetworkMode
		displays = oldDisplays
		volumes = oldVolumes
		shareDir = oldShareDir
		usbDevices = oldUSBDevices
		enableClipboard = oldEnableClipboard
		serialOutput = oldSerialOutput
	})

	vmDir = t.TempDir()
	runtimeProfile = "full"
	cpuCount = 4
	memoryGB = 8
	networkMode = "nat"
	displays = nil
	volumes = volumeSlice{{HostPath: "/tmp/work", Tag: "work"}}
	shareDir = ""
	usbDevices = USBStorageSlice{{Path: "/tmp/disk1.img"}}
	enableClipboard = true
	serialOutput = "stdout"

	got := currentConfigFingerprint()
	if got.DirectorySharingDevices != 2 {
		t.Fatalf("currentConfigFingerprint().DirectorySharingDevices = %d, want 2", got.DirectorySharingDevices)
	}
	if got.SocketDevices != 1 {
		t.Fatalf("currentConfigFingerprint().SocketDevices = %d, want 1", got.SocketDevices)
	}
	if got.BalloonDevices != 1 {
		t.Fatalf("currentConfigFingerprint().BalloonDevices = %d, want 1", got.BalloonDevices)
	}
	if !got.Clipboard {
		t.Fatal("currentConfigFingerprint().Clipboard = false, want true")
	}
	if !got.Serial {
		t.Fatal("currentConfigFingerprint().Serial = false, want true")
	}
}

func TestCheckSuspendConfigMatchDetectsUSBControllerChange(t *testing.T) {
	oldVMDir := vmDir
	oldCPUCount := cpuCount
	oldMemoryGB := memoryGB
	oldNetworkMode := networkMode
	oldDisplays := displays
	oldVolumes := volumes
	oldShareDir := shareDir
	oldUSBDevices := usbDevices
	oldEnableClipboard := enableClipboard
	oldSerialOutput := serialOutput
	oldRuntimeProfile := runtimeProfile

	t.Cleanup(func() {
		vmDir = oldVMDir
		cpuCount = oldCPUCount
		memoryGB = oldMemoryGB
		networkMode = oldNetworkMode
		displays = oldDisplays
		volumes = oldVolumes
		shareDir = oldShareDir
		usbDevices = oldUSBDevices
		enableClipboard = oldEnableClipboard
		serialOutput = oldSerialOutput
		runtimeProfile = oldRuntimeProfile
	})

	vmDir = t.TempDir()
	cpuCount = 2
	memoryGB = 4
	networkMode = "nat"
	displays = nil
	volumes = nil
	shareDir = ""
	usbDevices = nil
	enableClipboard = false
	serialOutput = "stdout"
	runtimeProfile = "full"

	legacyFingerprint := `{
  "cpus": 2,
  "memoryGB": 4,
  "network": "nat",
  "displays": 1,
  "volumes": 0,
  "directorySharingDevices": 1,
  "usbDevices": 0,
  "socketDevices": 1,
  "balloonDevices": 1,
  "clipboard": false,
  "serial": true
}`
	if err := os.WriteFile(filepath.Join(vmDir, "suspend.config.json"), []byte(legacyFingerprint), 0644); err != nil {
		t.Fatalf("write suspend config: %v", err)
	}

	err := checkSuspendConfigMatch()
	if err == nil {
		t.Fatal("checkSuspendConfigMatch() = nil, want mismatch error")
	}
	if !strings.Contains(err.Error(), "USB controllers: 0 -> 1") {
		t.Fatalf("checkSuspendConfigMatch() = %q, want USB controller mismatch", err)
	}
}

func TestCheckSuspendConfigMatchDetectsVirtioRuntimeSurfaceChange(t *testing.T) {
	oldVMDir := vmDir
	oldRuntimeProfile := runtimeProfile
	oldCPUCount := cpuCount
	oldMemoryGB := memoryGB
	oldNetworkMode := networkMode
	oldDisplays := displays
	oldVolumes := volumes
	oldShareDir := shareDir
	oldUSBDevices := usbDevices
	oldEnableClipboard := enableClipboard
	oldSerialOutput := serialOutput
	t.Cleanup(func() {
		vmDir = oldVMDir
		runtimeProfile = oldRuntimeProfile
		cpuCount = oldCPUCount
		memoryGB = oldMemoryGB
		networkMode = oldNetworkMode
		displays = oldDisplays
		volumes = oldVolumes
		shareDir = oldShareDir
		usbDevices = oldUSBDevices
		enableClipboard = oldEnableClipboard
		serialOutput = oldSerialOutput
	})

	vmDir = t.TempDir()
	runtimeProfile = "minimal"
	cpuCount = 2
	memoryGB = 4
	networkMode = "nat"
	displays = nil
	volumes = nil
	shareDir = ""
	usbDevices = nil
	enableClipboard = false
	serialOutput = "none"

	savedFingerprint := `{
  "cpus": 2,
  "memoryGB": 4,
  "network": "nat",
  "displays": 1,
  "volumes": 0,
  "directorySharingDevices": 1,
  "usbDevices": 0,
  "usbControllers": 1,
  "socketDevices": 1,
  "balloonDevices": 1,
  "clipboard": false,
  "serial": false
}`
	if err := os.WriteFile(filepath.Join(vmDir, "suspend.config.json"), []byte(savedFingerprint), 0644); err != nil {
		t.Fatalf("write suspend config: %v", err)
	}

	err := checkSuspendConfigMatch()
	if err == nil {
		t.Fatal("checkSuspendConfigMatch() = nil, want mismatch error")
	}
	for _, want := range []string{
		"directory sharing devices: 1 -> 0",
		"socket devices: 1 -> 0",
		"balloon devices: 1 -> 0",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("checkSuspendConfigMatch() = %q, want substring %q", err, want)
		}
	}
}
