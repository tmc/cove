package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/tmc/apple/virtualization"
	"github.com/tmc/cove/internal/vmpolicy"
)

type fakeLifecycleClock struct {
	now time.Time
}

func (f fakeLifecycleClock) Now() time.Time { return f.now }

func (f fakeLifecycleClock) NewTicker(time.Duration) lifecycleTicker {
	return fakeLifecycleTicker{ch: make(chan time.Time)}
}

type fakeLifecycleTicker struct {
	ch chan time.Time
}

func (t fakeLifecycleTicker) C() <-chan time.Time { return t.ch }

func (t fakeLifecycleTicker) Stop() {}

func TestCheckVMLifecyclePolicyStopsForIdle(t *testing.T) {
	withTempHome(t)
	runsRoot := t.TempDir()
	prevRuns := runsDirHook
	runsDirHook = func() string { return runsRoot }
	t.Cleanup(func() { runsDirHook = prevRuns })

	prevClock := vmLifecycleClock
	vmLifecycleClock = fakeLifecycleClock{now: time.Unix(4000, 0)}
	t.Cleanup(func() { vmLifecycleClock = prevClock })

	prevStateHook := lifecycleCurrentVMStateHook
	lifecycleCurrentVMStateHook = func(*ControlServer) (virtualization.VZVirtualMachineState, error) {
		return virtualization.VZVirtualMachineStateRunning, nil
	}
	t.Cleanup(func() { lifecycleCurrentVMStateHook = prevStateHook })

	prevStopHook := lifecycleRequestStopHook
	stopped := false
	lifecycleRequestStopHook = func(*ControlServer) error {
		stopped = true
		return nil
	}
	t.Cleanup(func() { lifecycleRequestStopHook = prevStopHook })

	dir := t.TempDir()
	if err := vmpolicy.Save(dir, vmpolicy.Policy{IdleTimeout: 30 * time.Minute}); err != nil {
		t.Fatalf("Save(): %v", err)
	}
	s := NewControlServerWithVMDir("", dir)
	s.setPolicyStartTime(time.Unix(0, 0))
	s.bridge.SetLastPingForTest(time.Unix(0, 0))

	run, err := beginStandaloneMetricsRun("vm-idle", "")
	if err != nil {
		t.Fatalf("beginStandaloneMetricsRun: %v", err)
	}
	defer finishStandaloneMetricsRun(run)

	s.checkVMLifecyclePolicy()
	if !stopped {
		t.Fatal("policy stop did not request shutdown")
	}
	events := readMetricEvents(t, filepath.Join(run.dir, "metrics.jsonl"))
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	got := events[0]
	if got.EventType != "lifecycle.idle.tripped" || got.Status != "tripped" {
		t.Fatalf("event = %+v", got)
	}
	if got.Extra["reason"] != "idle" {
		t.Fatalf("reason = %#v, want idle", got.Extra["reason"])
	}
	if got.Extra["idle_timeout_s"] != float64(1800) {
		t.Fatalf("idle_timeout_s = %#v, want 1800", got.Extra["idle_timeout_s"])
	}
}

func TestCheckVMLifecyclePolicyStopsForMaxAge(t *testing.T) {
	withTempHome(t)
	runsRoot := t.TempDir()
	prevRuns := runsDirHook
	runsDirHook = func() string { return runsRoot }
	t.Cleanup(func() { runsDirHook = prevRuns })

	prevClock := vmLifecycleClock
	vmLifecycleClock = fakeLifecycleClock{now: time.Unix(2000, 0)}
	t.Cleanup(func() { vmLifecycleClock = prevClock })

	prevStateHook := lifecycleCurrentVMStateHook
	lifecycleCurrentVMStateHook = func(*ControlServer) (virtualization.VZVirtualMachineState, error) {
		return virtualization.VZVirtualMachineStateRunning, nil
	}
	t.Cleanup(func() { lifecycleCurrentVMStateHook = prevStateHook })

	prevStopHook := lifecycleRequestStopHook
	stopped := false
	lifecycleRequestStopHook = func(*ControlServer) error {
		stopped = true
		return nil
	}
	t.Cleanup(func() { lifecycleRequestStopHook = prevStopHook })

	dir := t.TempDir()
	if err := vmpolicy.Save(dir, vmpolicy.Policy{MaxAge: 30 * time.Minute}); err != nil {
		t.Fatalf("Save(): %v", err)
	}
	s := NewControlServerWithVMDir("", dir)
	s.setPolicyStartTime(time.Unix(0, 0))
	run, err := beginStandaloneMetricsRun("vm-age", "")
	if err != nil {
		t.Fatalf("beginStandaloneMetricsRun: %v", err)
	}
	defer finishStandaloneMetricsRun(run)
	s.checkVMLifecyclePolicy()
	if !stopped {
		t.Fatal("policy stop did not request shutdown")
	}
	events := readMetricEvents(t, filepath.Join(run.dir, "metrics.jsonl"))
	if len(events) != 1 || events[0].EventType != "lifecycle.maxage.tripped" || events[0].Extra["reason"] != "max_age" {
		t.Fatalf("events = %+v", events)
	}
	if events[0].Extra["max_age_s"] != float64(1800) {
		t.Fatalf("max_age_s = %#v, want 1800", events[0].Extra["max_age_s"])
	}
}
