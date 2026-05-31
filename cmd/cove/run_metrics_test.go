package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

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
		return &controlpb.AgentInfoResponse{RawJson: `{"memory_total":8192,"memory_available":4096,"disk_total":65536,"disk_available":32768,"load_avg_1":1.25,"load_avg_5":"0.75","load_avg_15":0.5,"uptime_seconds":99,"users":["dev","ci"],"process_count":22,"top_processes":[{"pid":301,"cpu_percent":35.5,"rss_bytes":65536,"command":"make"},{"pid":302,"cpuPercent":3.5,"rssBytes":"32768","command":"sh"}]}`}, nil
	}
	prevMemory := resourceMemoryInfoHook
	resourceMemoryInfoHook = func(string) (*controlpb.MemoryInfoResponse, error) {
		return &controlpb.MemoryInfoResponse{Info: &controlpb.MemoryInfo{
			ConfiguredGb:     8,
			TargetGb:         6,
			MinimumAllowedMb: 1024,
			HasBalloon:       true,
		}}, nil
	}
	prevServer := resourceServerInfoHook
	resourceServerInfoHook = func(string) (RuntimeServerInfo, bool) {
		return RuntimeServerInfo{PID: 123, StartSource: "cove run"}, true
	}
	prevUsage := resourceProcessUsageHook
	resourceProcessUsageHook = func(int) (resourceProcessUsage, bool) {
		return resourceProcessUsage{CPUPercent: 12.5, RSSBytes: 42 * 1024}, true
	}
	t.Cleanup(func() {
		runsDirHook = prevRuns
		resourceAgentInfoHook = prevInfo
		resourceMemoryInfoHook = prevMemory
		resourceServerInfoHook = prevServer
		resourceProcessUsageHook = prevUsage
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
	if got.Extra["disk_total_bytes"].(float64) != 65536 || got.Extra["disk_available_bytes"].(float64) != 32768 || got.Extra["guest_uptime_seconds"].(float64) != 99 {
		t.Fatalf("guest disk/uptime extra = %#v", got.Extra)
	}
	if got.Extra["guest_load_avg_1"].(float64) != 1.25 || got.Extra["guest_load_avg_5"].(float64) != 0.75 || got.Extra["guest_load_avg_15"].(float64) != 0.5 || got.Extra["guest_user_count"].(float64) != 2 {
		t.Fatalf("guest load/user extra = %#v", got.Extra)
	}
	if got.Extra["guest_process_count"].(float64) != 22 {
		t.Fatalf("guest process count = %#v", got.Extra)
	}
	top := got.Extra["guest_top_processes"].([]any)
	if len(top) != 2 || top[0].(map[string]any)["command"] != "make" || top[0].(map[string]any)["cpu_percent"].(float64) != 35.5 {
		t.Fatalf("guest top processes = %#v", got.Extra["guest_top_processes"])
	}
	if got.Extra["configured_memory_gb"].(float64) != 8 || got.Extra["target_memory_gb"].(float64) != 6 || got.Extra["minimum_allowed_memory_mb"].(float64) != 1024 || got.Extra["has_balloon"].(bool) != true {
		t.Fatalf("vz memory extra = %#v", got.Extra)
	}
	if got.Extra["host_pid"].(float64) != 123 || got.Extra["host_cpu_percent"].(float64) != 12.5 || got.Extra["host_rss_bytes"].(float64) != 42*1024 || got.Extra["host_start_source"] != "cove run" {
		t.Fatalf("host extra = %#v", got.Extra)
	}
	if events[1].Extra["phase"] != "end" {
		t.Fatalf("end extra = %#v", events[1].Extra)
	}
	run.EmitResourceSampleMetric("end")
	events = readMetricEvents(t, filepath.Join(run.dir, "metrics.jsonl"))
	if len(events) != 2 {
		t.Fatalf("events after duplicate end = %d, want 2", len(events))
	}
}

func TestRunBundleResourceSampleUsesChildVMDir(t *testing.T) {
	withTempHome(t)
	runsRoot := t.TempDir()
	prevInfo := resourceAgentInfoHook
	prevMemory := resourceMemoryInfoHook
	prevServer := resourceServerInfoHook
	prevUsage := resourceProcessUsageHook
	var agentDir, memoryDir, serverDir string
	resourceAgentInfoHook = func(vmDir string) (*controlpb.AgentInfoResponse, error) {
		agentDir = vmDir
		return &controlpb.AgentInfoResponse{RawJson: `{"memoryTotal":"16384","memoryAvailable":"8192","diskTotal":"131072","diskAvailable":"65536","loadAvg1":"2.5","loadAvg5":1.5,"loadAvg15":0.25,"uptimeSeconds":"123","users":["dev"],"processCount":7,"topProcesses":[{"pid":401,"cpuPercent":"12.5","rssBytes":4096,"command":"xcodebuild"}]}`}, nil
	}
	resourceMemoryInfoHook = func(vmDir string) (*controlpb.MemoryInfoResponse, error) {
		memoryDir = vmDir
		return &controlpb.MemoryInfoResponse{Info: &controlpb.MemoryInfo{ConfiguredGb: 16}}, nil
	}
	resourceServerInfoHook = func(vmDir string) (RuntimeServerInfo, bool) {
		serverDir = vmDir
		return RuntimeServerInfo{PID: 456}, true
	}
	resourceProcessUsageHook = func(int) (resourceProcessUsage, bool) {
		return resourceProcessUsage{CPUPercent: 1.25, RSSBytes: 64 * 1024}, true
	}
	t.Cleanup(func() {
		resourceAgentInfoHook = prevInfo
		resourceMemoryInfoHook = prevMemory
		resourceServerInfoHook = prevServer
		resourceProcessUsageHook = prevUsage
	})

	b, err := NewRunBundle(runsRoot, "child", "base:latest")
	if err != nil {
		t.Fatalf("NewRunBundle: %v", err)
	}
	b.SetVMDir("/tmp/child-vm")
	b.MarkAgentReady()
	b.EmitResourceSampleMetric("end")
	if err := b.Finalize(nil); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	events := readMetricEvents(t, filepath.Join(b.Dir(), "metrics.jsonl"))
	var samples []runmetrics.Event
	for _, e := range events {
		if e.EventType == "resource_sample" {
			samples = append(samples, e)
		}
	}
	if len(samples) != 2 {
		t.Fatalf("resource samples = %d in %+v, want 2", len(samples), events)
	}
	if agentDir != "/tmp/child-vm" || memoryDir != "/tmp/child-vm" || serverDir != "/tmp/child-vm" {
		t.Fatalf("hook dirs agent=%q memory=%q server=%q, want child vm dir", agentDir, memoryDir, serverDir)
	}
	if samples[0].Extra["phase"] != "start" || samples[0].Extra["memory_total_bytes"].(float64) != 16384 || samples[0].Extra["memory_available_bytes"].(float64) != 8192 {
		t.Fatalf("start sample = %#v", samples[0].Extra)
	}
	if samples[0].Extra["disk_total_bytes"].(float64) != 131072 || samples[0].Extra["disk_available_bytes"].(float64) != 65536 || samples[0].Extra["guest_load_avg_1"].(float64) != 2.5 || samples[0].Extra["guest_user_count"].(float64) != 1 {
		t.Fatalf("start guest counters = %#v", samples[0].Extra)
	}
	if samples[0].Extra["guest_process_count"].(float64) != 7 || samples[0].Extra["guest_top_processes"].([]any)[0].(map[string]any)["command"] != "xcodebuild" {
		t.Fatalf("start guest processes = %#v", samples[0].Extra)
	}
	if samples[1].Extra["phase"] != "end" || samples[1].Extra["configured_memory_gb"].(float64) != 16 {
		t.Fatalf("end sample = %#v", samples[1].Extra)
	}
	if samples[1].Extra["host_cpu_percent"].(float64) != 1.25 || samples[1].Extra["host_rss_bytes"].(float64) != 64*1024 {
		t.Fatalf("end host sample = %#v", samples[1].Extra)
	}
}

func TestRunBundlePeriodicResourceSamples(t *testing.T) {
	withTempHome(t)
	withResourceSampleInterval(t, 5*time.Millisecond)
	runsRoot := t.TempDir()
	prevInfo := resourceAgentInfoHook
	prevMemory := resourceMemoryInfoHook
	prevServer := resourceServerInfoHook
	prevUsage := resourceProcessUsageHook
	calls := make(chan struct{}, 16)
	resourceAgentInfoHook = func(string) (*controlpb.AgentInfoResponse, error) {
		select {
		case calls <- struct{}{}:
		default:
		}
		return &controlpb.AgentInfoResponse{RawJson: `{"memoryTotal":"32768","memoryAvailable":"16384","loadAvg1":3.25,"uptimeSeconds":10,"processCount":3,"topProcesses":[{"pid":501,"cpuPercent":4.5,"rssBytes":8192,"command":"swift"}]}`}, nil
	}
	resourceMemoryInfoHook = func(string) (*controlpb.MemoryInfoResponse, error) { return nil, nil }
	resourceServerInfoHook = func(string) (RuntimeServerInfo, bool) {
		return RuntimeServerInfo{PID: 789}, true
	}
	resourceProcessUsageHook = func(int) (resourceProcessUsage, bool) {
		return resourceProcessUsage{CPUPercent: 3.5, RSSBytes: 128 * 1024}, true
	}
	t.Cleanup(func() {
		resourceAgentInfoHook = prevInfo
		resourceMemoryInfoHook = prevMemory
		resourceServerInfoHook = prevServer
		resourceProcessUsageHook = prevUsage
	})

	b, err := NewRunBundle(runsRoot, "child", "base:latest")
	if err != nil {
		t.Fatalf("NewRunBundle: %v", err)
	}
	b.SetVMDir("/tmp/child-vm")
	b.MarkAgentReady()
	waitResourceHookCalls(t, calls, 2)
	b.EmitResourceSampleMetric("end")
	if err := b.Finalize(nil); err != nil {
		t.Fatalf("Finalize: %v", err)
	}

	events := readMetricEvents(t, filepath.Join(b.Dir(), "metrics.jsonl"))
	if !sawResourcePhase(events, "start") || !sawResourcePhase(events, "periodic") || !sawResourcePhase(events, "end") {
		t.Fatalf("events = %+v, want start, periodic, and end resource samples", events)
	}
	for _, e := range events {
		if e.EventType == "resource_sample" && e.Extra["phase"] == "periodic" {
			if e.Extra["sample_index"].(float64) < 1 {
				t.Fatalf("periodic sample index = %#v", e.Extra)
			}
			if e.Extra["memory_total_bytes"].(float64) != 32768 {
				t.Fatalf("periodic sample = %#v", e.Extra)
			}
			if e.Extra["guest_load_avg_1"].(float64) != 3.25 || e.Extra["guest_uptime_seconds"].(float64) != 10 {
				t.Fatalf("periodic guest counters = %#v", e.Extra)
			}
			if e.Extra["guest_process_count"].(float64) != 3 || e.Extra["guest_top_processes"].([]any)[0].(map[string]any)["command"] != "swift" {
				t.Fatalf("periodic guest processes = %#v", e.Extra)
			}
			if e.Extra["host_cpu_percent"].(float64) != 3.5 || e.Extra["host_rss_bytes"].(float64) != 128*1024 {
				t.Fatalf("periodic host sample = %#v", e.Extra)
			}
		}
	}
}

func TestStandaloneResourceSamplerStopsBeforeClose(t *testing.T) {
	withTempHome(t)
	withResourceSampleInterval(t, 5*time.Millisecond)
	runsRoot := t.TempDir()
	prevRuns := runsDirHook
	runsDirHook = func() string { return runsRoot }
	prevInfo := resourceAgentInfoHook
	prevMemory := resourceMemoryInfoHook
	prevServer := resourceServerInfoHook
	prevUsage := resourceProcessUsageHook
	calls := make(chan struct{}, 16)
	resourceAgentInfoHook = func(string) (*controlpb.AgentInfoResponse, error) {
		select {
		case calls <- struct{}{}:
		default:
		}
		return &controlpb.AgentInfoResponse{RawJson: `{"memory_total":4096,"memory_available":2048,"load_avg_1":0.1}`}, nil
	}
	resourceMemoryInfoHook = func(string) (*controlpb.MemoryInfoResponse, error) { return nil, nil }
	resourceServerInfoHook = func(string) (RuntimeServerInfo, bool) { return RuntimeServerInfo{}, false }
	resourceProcessUsageHook = func(int) (resourceProcessUsage, bool) { return resourceProcessUsage{}, false }
	t.Cleanup(func() {
		runsDirHook = prevRuns
		resourceAgentInfoHook = prevInfo
		resourceMemoryInfoHook = prevMemory
		resourceServerInfoHook = prevServer
		resourceProcessUsageHook = prevUsage
	})

	run, err := beginStandaloneMetricsRun("vm-x", "", "/tmp/vm-x")
	if err != nil {
		t.Fatalf("beginStandaloneMetricsRun: %v", err)
	}
	run.MarkAgentReady()
	waitResourceHookCalls(t, calls, 2)
	finishStandaloneMetricsRun(run)
	events := readMetricEvents(t, filepath.Join(run.dir, "metrics.jsonl"))
	count := len(events)
	if !sawResourcePhase(events, "periodic") || !sawResourcePhase(events, "end") {
		t.Fatalf("events = %+v, want periodic and end samples", events)
	}
	time.Sleep(15 * time.Millisecond)
	events = readMetricEvents(t, filepath.Join(run.dir, "metrics.jsonl"))
	if len(events) != count {
		t.Fatalf("events after finish = %d, want still %d: %+v", len(events), count, events)
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

func withResourceSampleInterval(t *testing.T, interval time.Duration) {
	t.Helper()
	prev := resourceSampleInterval
	resourceSampleInterval = interval
	t.Cleanup(func() { resourceSampleInterval = prev })
}

func waitResourceHookCalls(t *testing.T, calls <-chan struct{}, n int) {
	t.Helper()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for i := 0; i < n; i++ {
		select {
		case <-calls:
		case <-timer.C:
			t.Fatalf("resource hook calls = %d, want %d", i, n)
		}
	}
}

func sawResourcePhase(events []runmetrics.Event, phase string) bool {
	for _, e := range events {
		if e.EventType == "resource_sample" && e.Extra["phase"] == phase {
			return true
		}
	}
	return false
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

func metricEventOfType(events []runmetrics.Event, typ string) *runmetrics.Event {
	for i := range events {
		if events[i].EventType == typ {
			return &events[i]
		}
	}
	return nil
}
