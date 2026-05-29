package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/cove/internal/controlserver"
	runmetrics "github.com/tmc/cove/internal/metrics"
	"github.com/tmc/cove/internal/vmrun"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestStandaloneMetricsRunWritesJSONL(t *testing.T) {
	withTempHome(t)
	runsRoot := t.TempDir()
	prev := runsDirHook
	runsDirHook = func() string { return runsRoot }
	t.Cleanup(func() {
		runsDirHook = prev
	})

	run, err := beginStandaloneMetricsRun("vm-x", "image:ci")
	if err != nil {
		t.Fatalf("beginStandaloneMetricsRun: %v", err)
	}
	run.EmitMetricEvent("vm_start", run.started, "ok", map[string]any{"mode": "test"})
	finishStandaloneMetricsRun(run)

	events := readMetricEvents(t, filepath.Join(run.dir, "metrics.jsonl"))
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	got := events[0]
	if got.EventType != "vm_start" || got.VMName != "vm-x" || got.ImageRef != "image:ci" || got.Status != "ok" {
		t.Fatalf("event = %+v", got)
	}
	if got.DurationMS < 0 {
		t.Fatalf("duration_ms = %d, want non-negative", got.DurationMS)
	}
	if got.Extra["run_id"] != run.id || got.Extra["mode"] != "test" {
		t.Fatalf("extra = %#v", got.Extra)
	}
}

func TestMetricEventTypes(t *testing.T) {
	withTempHome(t)
	runsRoot := t.TempDir()
	prev := runsDirHook
	runsDirHook = func() string { return runsRoot }
	t.Cleanup(func() {
		runsDirHook = prev
	})

	run, err := beginStandaloneMetricsRun("vm-x", "image:ci")
	if err != nil {
		t.Fatalf("beginStandaloneMetricsRun: %v", err)
	}
	started := run.started
	for _, typ := range []string{"vm_create", "vm_start", "agent_ready", "fork_created", "build_step", "run_complete"} {
		run.EmitMetricEvent(typ, started, "ok", map[string]any{"source": "test"})
	}
	finishStandaloneMetricsRun(run)

	events := readMetricEvents(t, filepath.Join(run.dir, "metrics.jsonl"))
	if len(events) != 6 {
		t.Fatalf("events = %d, want 6", len(events))
	}
	seen := map[string]bool{}
	for _, e := range events {
		seen[e.EventType] = true
		if e.Status != "ok" {
			t.Fatalf("%s status = %q, want ok", e.EventType, e.Status)
		}
	}
	for _, typ := range []string{"vm_create", "vm_start", "agent_ready", "fork_created", "build_step", "run_complete"} {
		if !seen[typ] {
			t.Fatalf("missing event type %q in %+v", typ, events)
		}
	}
}

func TestCaptureLatencyMetricWritesJSONL(t *testing.T) {
	withTempHome(t)
	runsRoot := t.TempDir()
	prev := runsDirHook
	runsDirHook = func() string { return runsRoot }
	t.Cleanup(func() {
		runsDirHook = prev
	})

	run, err := beginStandaloneMetricsRun("vm-x", "image:ci")
	if err != nil {
		t.Fatalf("beginStandaloneMetricsRun: %v", err)
	}
	run.EmitCaptureLatency(context.Background(), controlserver.CaptureLatencyEvent{
		Backend:          "sckit",
		RequestedBackend: "sckit",
		Width:            640,
		Height:           480,
		DurationMS:       12,
		Status:           "ok",
	})
	finishStandaloneMetricsRun(run)

	events := readMetricEvents(t, filepath.Join(run.dir, "metrics.jsonl"))
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	got := events[0]
	if got.EventType != "capture_latency" || got.Status != "ok" || got.DurationMS < 12 {
		t.Fatalf("event = %+v", got)
	}
	if got.Extra["backend"] != "sckit" || got.Extra["requested_backend"] != "sckit" || got.Extra["run_id"] != run.id {
		t.Fatalf("extra = %#v", got.Extra)
	}
	if got.Extra["width"].(float64) != 640 || got.Extra["height"].(float64) != 480 {
		t.Fatalf("extra size = %#v", got.Extra)
	}
}

func TestResourceSampleMetricWritesMemory(t *testing.T) {
	withTempHome(t)
	runsRoot := t.TempDir()
	prevRuns := runsDirHook
	runsDirHook = func() string { return runsRoot }
	prevInfo := resourceAgentInfoHook
	resourceAgentInfoHook = func(string) (*controlpb.AgentInfoResponse, error) {
		return &controlpb.AgentInfoResponse{RawJson: `{"memory_total":8192,"memory_available":4096}`}, nil
	}
	t.Cleanup(func() {
		runsDirHook = prevRuns
		resourceAgentInfoHook = prevInfo
	})

	run, err := beginStandaloneMetricsRun("vm-x", "", "/tmp/vm-x")
	if err != nil {
		t.Fatalf("beginStandaloneMetricsRun: %v", err)
	}
	run.EmitResourceSampleMetric("start")
	finishStandaloneMetricsRun(run)

	events := readMetricEvents(t, filepath.Join(run.dir, "metrics.jsonl"))
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2", len(events))
	}
	got := events[0]
	if got.EventType != "resource_sample" || got.Status != "ok" {
		t.Fatalf("event = %+v", got)
	}
	if got.Extra["phase"] != "start" || got.Extra["memory_total_bytes"].(float64) != 8192 || got.Extra["memory_available_bytes"].(float64) != 4096 {
		t.Fatalf("extra = %#v", got.Extra)
	}
	if events[1].Extra["phase"] != "end" {
		t.Fatalf("end extra = %#v", events[1].Extra)
	}
}

func TestRunVMWithConfigEmitsRunComplete(t *testing.T) {
	withTempHome(t)
	runsRoot := t.TempDir()
	prevRuns := runsDirHook
	runsDirHook = func() string { return runsRoot }
	t.Cleanup(func() { runsDirHook = prevRuns })
	hooks, _ := stubAcquireRunLockHook(t)
	hooks.RunMacOSVM = func(_ vmrun.RunConfig, _ vmrun.HostConfig, _ *RunBundle, recorder runMetricRecorder) error {
		recorder.MarkAgentReady()
		return nil
	}

	cfg := RunConfig{VM: vmSelection{Name: "plain-vm", Directory: t.TempDir()}, Hooks: hooks}
	if err := runVMWithConfig(cfg); err != nil {
		t.Fatalf("runVMWithConfig: %v", err)
	}
	entries, err := os.ReadDir(runsRoot)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("metrics dirs = %d, want 1", len(entries))
	}
	events := readMetricEvents(t, filepath.Join(runsRoot, entries[0].Name(), "metrics.jsonl"))
	var sawReady, sawComplete bool
	for _, e := range events {
		if e.EventType == "agent_ready" && e.Status == "ok" {
			sawReady = true
		}
		if e.EventType == "run_complete" && e.Status == "ok" && e.VMName == "plain-vm" {
			sawComplete = true
		}
	}
	if !sawReady || !sawComplete {
		t.Fatalf("events = %+v, want agent_ready and run_complete", events)
	}
}

func readMetricEvents(t *testing.T, path string) []runmetrics.Event {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()
	var events []runmetrics.Event
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		var e runmetrics.Event
		if err := json.Unmarshal(scan.Bytes(), &e); err != nil {
			t.Fatalf("unmarshal %q: %v", scan.Text(), err)
		}
		events = append(events, e)
	}
	if err := scan.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return events
}
