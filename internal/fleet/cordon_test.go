// cove fleet control plane (paid layer) — see docs/designs/046-fleet-control-plane.md
package fleet

import "testing"

func TestCordonRemovesHostFromPlacement(t *testing.T) {
	reg := newSchedRegistry(t,
		&Host{HostID: "host-a", Arch: "arm64", FreeRAMBytes: 16 * gib},
		&Host{HostID: "host-b", Arch: "arm64", FreeRAMBytes: 16 * gib},
	)

	// Cordon host-a; placement must pick host-b.
	if err := reg.Cordon("host-a"); err != nil {
		t.Fatalf("cordon: %v", err)
	}
	p, token, err := reg.Schedule(ScheduleRequest{RequestedRAM: gib, BaseRef: "base:latest"})
	if err != nil {
		t.Fatalf("schedule: %v", err)
	}
	reg.ReleaseReservation(token)
	if p.Chosen != "host-b" {
		t.Errorf("chosen = %q, want host-b (host-a cordoned)", p.Chosen)
	}

	// Uncordon host-a, cordon host-b: the only feasible host is host-a.
	reg.Uncordon("host-a")
	if err := reg.Cordon("host-b"); err != nil {
		t.Fatalf("cordon b: %v", err)
	}
	p, token, err = reg.Schedule(ScheduleRequest{RequestedRAM: gib, BaseRef: "base:latest"})
	if err != nil {
		t.Fatalf("schedule after uncordon: %v", err)
	}
	reg.ReleaseReservation(token)
	if p.Chosen != "host-a" {
		t.Errorf("chosen = %q, want host-a", p.Chosen)
	}
}

func TestCordonUnregisteredHostErrors(t *testing.T) {
	reg := newSchedRegistry(t)
	if err := reg.Cordon("ghost"); err == nil {
		t.Errorf("cordon of unregistered host did not error")
	}
}

func TestCordonAllHostsLeavesNoFeasible(t *testing.T) {
	reg := newSchedRegistry(t, &Host{HostID: "host-a", Arch: "arm64", FreeRAMBytes: 16 * gib})
	if err := reg.Cordon("host-a"); err != nil {
		t.Fatalf("cordon: %v", err)
	}
	if _, _, err := reg.Schedule(ScheduleRequest{RequestedRAM: gib, BaseRef: "base:latest"}); err == nil {
		t.Errorf("schedule with all hosts cordoned did not error")
	}
}

func TestUncordonUnknownHostNoOp(t *testing.T) {
	reg := newSchedRegistry(t)
	reg.Uncordon("ghost") // must not panic
}
