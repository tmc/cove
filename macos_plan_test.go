package main

import (
	"testing"
	"time"

	"github.com/tmc/vz-macos/internal/vmrun"
)

func validMacOSPlanRunConfig() vmrun.RunConfig {
	return vmrun.RunConfig{
		OS:           vmrun.GuestMacOS,
		CPUCount:     4,
		MemoryGB:     8,
		DiskPath:     "/tmp/disk.img",
		NetworkMode:  "nat",
		StartTimeout: 30 * time.Second,
	}
}

func validMacOSPlanHostConfig() vmrun.HostConfig {
	return vmrun.HostConfig{
		VMDir:  "/tmp/cove-test-vm",
		VMName: "cove-test-vm",
	}
}

func TestMacOSDevicePlanDefaultsDisplay(t *testing.T) {
	plan, err := macOSDevicePlan(validMacOSPlanRunConfig(), validMacOSPlanHostConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Display) != 1 {
		t.Fatalf("display count = %d, want 1", len(plan.Display))
	}
	want := vmrun.DisplaySpec{Width: 1920, Height: 1200, PPI: 144}
	if plan.Display[0] != want {
		t.Fatalf("display = %+v, want %+v", plan.Display[0], want)
	}
}

func TestMacOSDevicePlanPreservesDisplay(t *testing.T) {
	rc := validMacOSPlanRunConfig()
	rc.Displays = []vmrun.DisplaySpec{{Width: 1280, Height: 800, PPI: 110}}
	plan, err := macOSDevicePlan(rc, validMacOSPlanHostConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Display) != 1 || plan.Display[0] != rc.Displays[0] {
		t.Fatalf("display = %+v, want %+v", plan.Display, rc.Displays)
	}
}
