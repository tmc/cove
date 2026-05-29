package main

import (
	"testing"

	"github.com/tmc/cove/internal/vmrun"
)

func TestVmrunRunConfigPopulatesCollectionLoops(t *testing.T) {
	saveVolumes := volumes
	saveUSB := usbDevices
	saveBlock := blockDevices
	saveStartup := startupPortForwards
	t.Cleanup(func() {
		volumes = saveVolumes
		usbDevices = saveUSB
		blockDevices = saveBlock
		startupPortForwards = saveStartup
	})

	volumes = volumeSlice{
		{HostPath: "/tmp/work", Tag: "work", ReadOnly: false},
		{HostPath: "/tmp/cache", Tag: "cache", ReadOnly: true},
	}
	usbDevices = USBStorageSlice{{Path: "/tmp/usb.img", ReadOnly: true}}
	blockDevices = blockDeviceSlice{{Path: "/dev/rdisk8", ReadOnly: false, Sync: "full"}}
	startupPortForwards = portForwardSpecs{{HostPort: 18080, GuestPort: 80}}

	rc := vmrunRunConfig(vmrun.GuestLinux)

	if len(rc.Volumes) != 2 {
		t.Fatalf("rc.Volumes len = %d, want 2", len(rc.Volumes))
	}
	if rc.Volumes[1].Tag != "cache" || !rc.Volumes[1].ReadOnly {
		t.Fatalf("rc.Volumes[1] = %#v, want cache ro", rc.Volumes[1])
	}
	if len(rc.USB) != 1 || rc.USB[0].Path != "/tmp/usb.img" || !rc.USB[0].ReadOnly {
		t.Fatalf("rc.USB = %#v, want one ro entry", rc.USB)
	}
	if len(rc.BlockDevices) != 1 || rc.BlockDevices[0].Cache != "full" {
		t.Fatalf("rc.BlockDevices = %#v, want one full-cache entry", rc.BlockDevices)
	}
	if len(rc.StartupForwards) != 1 || rc.StartupForwards[0].HostPort != 18080 || rc.StartupForwards[0].GuestPort != 80 {
		t.Fatalf("rc.StartupForwards = %#v, want 18080->80", rc.StartupForwards)
	}
	if rc.OS != vmrun.GuestLinux {
		t.Fatalf("rc.OS = %v, want GuestLinux", rc.OS)
	}
}
