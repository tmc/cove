package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/vmrun"
)

func TestCurrentUSBControllerFingerprintCount(t *testing.T) {
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
			hc := vmrun.HostConfig{RuntimeProfile: tt.runtimeProfile}
			if got := usbControllerFingerprintCount(hc); got != tt.want {
				t.Fatalf("usbControllerFingerprintCount() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCurrentVirtioDeviceFingerprintCounts(t *testing.T) {
	tests := []struct {
		name           string
		runtimeProfile string
		volumes        []vmrun.VolumeMount
		wantDirSharing int
		wantSockets    int
		wantBalloon    int
	}{
		{name: "full default", runtimeProfile: "full", wantDirSharing: 1, wantSockets: 1, wantBalloon: 1},
		{
			name:           "full with tagged volumes",
			runtimeProfile: "full",
			volumes: []vmrun.VolumeMount{
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
			rc := vmrun.RunConfig{Volumes: tt.volumes}
			hc := vmrun.HostConfig{RuntimeProfile: tt.runtimeProfile}
			if got := directorySharingDeviceFingerprintCount(rc, hc); got != tt.wantDirSharing {
				t.Fatalf("directorySharingDeviceFingerprintCount() = %d, want %d", got, tt.wantDirSharing)
			}
			if got := socketDeviceFingerprintCount(hc); got != tt.wantSockets {
				t.Fatalf("socketDeviceFingerprintCount() = %d, want %d", got, tt.wantSockets)
			}
			if got := balloonDeviceFingerprintCount(hc); got != tt.wantBalloon {
				t.Fatalf("balloonDeviceFingerprintCount() = %d, want %d", got, tt.wantBalloon)
			}
		})
	}
}

func TestCurrentConfigFingerprintIncludesVirtioRuntimeSurface(t *testing.T) {
	oldBootMode := currentBootSessionMode()
	t.Cleanup(func() {
		setActiveBootSessionMode(oldBootMode)
	})

	setActiveBootSessionMode(bootSessionModeRecovery)

	rc := vmrun.RunConfig{
		CPUCount:        4,
		MemoryGB:        8,
		NetworkMode:     "nat",
		Volumes:         []vmrun.VolumeMount{{HostPath: "/tmp/work", Tag: "work"}},
		USB:             []vmrun.USBSpec{{Path: "/tmp/disk1.img"}},
		EnableClipboard: true,
		SerialOutput:    "stdout",
	}
	hc := vmrun.HostConfig{VMDir: t.TempDir(), RuntimeProfile: "full"}
	got := currentConfigFingerprintForRun(rc, hc)
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
	if got.BootMode != "recovery" {
		t.Fatalf("currentConfigFingerprint().BootMode = %q, want recovery", got.BootMode)
	}
}

func TestCheckSuspendConfigMatchDetectsUSBControllerChange(t *testing.T) {
	rc := vmrun.RunConfig{CPUCount: 2, MemoryGB: 4, NetworkMode: "nat", SerialOutput: "stdout"}
	hc := vmrun.HostConfig{VMDir: t.TempDir(), RuntimeProfile: "full"}

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
	if err := os.WriteFile(filepath.Join(hc.VMDir, "suspend.config.json"), []byte(legacyFingerprint), 0644); err != nil {
		t.Fatalf("write suspend config: %v", err)
	}

	err := checkSuspendConfigMatchForRun(rc, hc)
	if err == nil {
		t.Fatal("checkSuspendConfigMatch() = nil, want mismatch error")
	}
	if !strings.Contains(err.Error(), "USB controllers: 0 -> 1") {
		t.Fatalf("checkSuspendConfigMatch() = %q, want USB controller mismatch", err)
	}
}

func TestCheckSuspendConfigMatchDetectsVirtioRuntimeSurfaceChange(t *testing.T) {
	rc := vmrun.RunConfig{CPUCount: 2, MemoryGB: 4, NetworkMode: "nat", SerialOutput: "none"}
	hc := vmrun.HostConfig{VMDir: t.TempDir(), RuntimeProfile: "minimal"}

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
	if err := os.WriteFile(filepath.Join(hc.VMDir, "suspend.config.json"), []byte(savedFingerprint), 0644); err != nil {
		t.Fatalf("write suspend config: %v", err)
	}

	err := checkSuspendConfigMatchForRun(rc, hc)
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

func TestRunRequiresColdBoot(t *testing.T) {
	tests := []struct {
		name             string
		recovery         bool
		bootCommandsFile string
		forceDFU         bool
		want             bool
		wantReason       string
	}{
		{name: "normal", wantReason: "current run mode"},
		{name: "recovery", recovery: true, want: true, wantReason: "recovery mode"},
		{name: "boot commands", bootCommandsFile: "/tmp/script.vzscript", want: true, wantReason: "boot automation"},
		{name: "recovery with boot commands", recovery: true, bootCommandsFile: "/tmp/script.vzscript", want: true, wantReason: "recovery mode with boot automation"},
		{name: "private start", forceDFU: true, want: true, wantReason: "private macOS boot options"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := vmrun.RunConfig{
				RecoveryMode:     tt.recovery,
				ForceDFU:         tt.forceDFU,
				BootCommandsFile: tt.bootCommandsFile,
			}
			if got := runRequiresColdBootForRun(rc); got != tt.want {
				t.Fatalf("runRequiresColdBoot() = %v, want %v", got, tt.want)
			}
			if got := coldBootReasonForRun(rc); got != tt.wantReason {
				t.Fatalf("coldBootReason() = %q, want %q", got, tt.wantReason)
			}
		})
	}
}

func TestCheckSuspendConfigMatchTreatsMissingBootModeAsNormal(t *testing.T) {
	oldBootMode := currentBootSessionMode()
	t.Cleanup(func() {
		setActiveBootSessionMode(oldBootMode)
	})

	rc := vmrun.RunConfig{CPUCount: 2, MemoryGB: 4, NetworkMode: "nat", SerialOutput: "stdout"}
	hc := vmrun.HostConfig{VMDir: t.TempDir(), RuntimeProfile: "full"}
	setActiveBootSessionMode(bootSessionModeNormal)

	legacyFingerprint := `{
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
  "serial": true
}`
	if err := os.WriteFile(filepath.Join(hc.VMDir, "suspend.config.json"), []byte(legacyFingerprint), 0644); err != nil {
		t.Fatalf("write suspend config: %v", err)
	}

	if err := checkSuspendConfigMatchForRun(rc, hc); err != nil {
		t.Fatalf("checkSuspendConfigMatch() = %v, want nil", err)
	}
}

func TestCheckSuspendConfigMatchDetectsBootModeChange(t *testing.T) {
	oldBootMode := currentBootSessionMode()
	t.Cleanup(func() {
		setActiveBootSessionMode(oldBootMode)
	})

	rc := vmrun.RunConfig{CPUCount: 2, MemoryGB: 4, NetworkMode: "nat", SerialOutput: "stdout"}
	hc := vmrun.HostConfig{VMDir: t.TempDir(), RuntimeProfile: "full"}
	setActiveBootSessionMode(bootSessionModeNormal)

	fingerprint := `{
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
  "serial": true,
  "bootMode": "recovery"
}`
	if err := os.WriteFile(filepath.Join(hc.VMDir, "suspend.config.json"), []byte(fingerprint), 0644); err != nil {
		t.Fatalf("write suspend config: %v", err)
	}

	err := checkSuspendConfigMatchForRun(rc, hc)
	if err == nil {
		t.Fatal("checkSuspendConfigMatch() = nil, want mismatch error")
	}
	if !strings.Contains(err.Error(), "boot mode: recovery -> normal") {
		t.Fatalf("checkSuspendConfigMatch() = %q, want boot mode mismatch", err)
	}
}
