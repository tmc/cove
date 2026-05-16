package guestplan

import (
	"strings"
	"testing"
	"time"

	"github.com/tmc/vz-macos/internal/vmrun"
)

func validLinuxPlanRunConfig() vmrun.RunConfig {
	return vmrun.RunConfig{
		OS:           vmrun.GuestLinux,
		CPUCount:     2,
		MemoryGB:     4,
		DiskPath:     "/tmp/disk.img",
		NetworkMode:  "nat",
		StartTimeout: 30 * time.Second,
	}
}

func validLinuxPlanHostConfig() vmrun.HostConfig {
	return vmrun.HostConfig{
		VMDir:  "/tmp/cove-test-vm",
		VMName: "cove-test-vm",
	}
}

func TestLinuxDevicePlanDefaultsDisplay(t *testing.T) {
	plan, err := Linux(validLinuxPlanRunConfig(), validLinuxPlanHostConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Display) != 1 {
		t.Fatalf("display count = %d, want 1", len(plan.Display))
	}
	want := vmrun.DisplaySpec{Width: 1024, Height: 768, PPI: 144}
	if plan.Display[0] != want {
		t.Fatalf("display = %+v, want %+v", plan.Display[0], want)
	}
}

func TestLinuxDevicePlanPreservesDisplay(t *testing.T) {
	rc := validLinuxPlanRunConfig()
	rc.Displays = []vmrun.DisplaySpec{{Width: 1280, Height: 800, PPI: 110}}
	plan, err := Linux(rc, validLinuxPlanHostConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Display) != 1 || plan.Display[0] != rc.Displays[0] {
		t.Fatalf("display = %+v, want %+v", plan.Display, rc.Displays)
	}
}

func TestLinuxDevicePlanValidationError(t *testing.T) {
	rc := validLinuxPlanRunConfig()
	rc.MemoryGB = 0
	_, err := Linux(rc, validLinuxPlanHostConfig())
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "memory must") {
		t.Fatalf("error = %q, want memory validation", err)
	}
}
