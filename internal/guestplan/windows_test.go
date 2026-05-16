package guestplan

import (
	"testing"
	"time"

	"github.com/tmc/vz-macos/internal/vmrun"
)

func validWindowsPlanRunConfig() vmrun.RunConfig {
	return vmrun.RunConfig{
		OS:           vmrun.GuestWindows,
		CPUCount:     4,
		MemoryGB:     8,
		DiskPath:     "/tmp/disk.img",
		NetworkMode:  "nat",
		StartTimeout: 30 * time.Second,
	}
}

func validWindowsPlanHostConfig() vmrun.HostConfig {
	return vmrun.HostConfig{
		VMDir:  "/tmp/cove-test-vm",
		VMName: "cove-test-vm",
	}
}

func TestWindowsDevicePlanDefaultsDisplay(t *testing.T) {
	plan, err := Windows(validWindowsPlanRunConfig(), validWindowsPlanHostConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Display) != 1 {
		t.Fatalf("display count = %d, want 1", len(plan.Display))
	}
	want := vmrun.DisplaySpec{Width: 1920, Height: 1080, PPI: 144}
	if plan.Display[0] != want {
		t.Fatalf("display = %+v, want %+v", plan.Display[0], want)
	}
}

func TestWindowsDevicePlanPreservesDisplay(t *testing.T) {
	rc := validWindowsPlanRunConfig()
	rc.Displays = []vmrun.DisplaySpec{{Width: 1280, Height: 720, PPI: 110}}
	plan, err := Windows(rc, validWindowsPlanHostConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Display) != 1 || plan.Display[0] != rc.Displays[0] {
		t.Fatalf("display = %+v, want %+v", plan.Display, rc.Displays)
	}
}
