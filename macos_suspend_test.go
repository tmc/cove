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
  "usbDevices": 0,
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
