package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tmc/cove/internal/controlserver"
	runmetrics "github.com/tmc/cove/internal/metrics"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

type standaloneMetricsRun struct {
	id              string
	dir             string
	vmName          string
	vmDir           string
	imageRef        string
	started         time.Time
	sink            runmetrics.Sink
	mu              sync.Mutex
	agentOK         bool
	resourceSampled map[string]bool
}

type runMetricRecorder interface {
	EmitMetricEvent(eventType string, started time.Time, status string, extra map[string]any)
	MarkAgentReady()
	EmitCaptureLatency(context.Context, controlserver.CaptureLatencyEvent)
}

var resourceAgentInfoHook = func(vmDir string) (*controlpb.AgentInfoResponse, error) {
	client := NewControlClient(GetControlSocketPathForVM(vmDir))
	return client.AgentInfo()
}

var resourceMemoryInfoHook = func(vmDir string) (*controlpb.MemoryInfoResponse, error) {
	client := NewControlClient(GetControlSocketPathForVM(vmDir))
	return client.MemoryInfo()
}

func beginStandaloneMetricsRun(vmName, imageRef string, vmDir ...string) (*standaloneMetricsRun, error) {
	id, err := generateRunID()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(runsDirHook(), id)
	sink, err := newMetricsSink(filepath.Join(dir, "metrics.jsonl"))
	if err != nil {
		return nil, err
	}
	run := &standaloneMetricsRun{
		id:       id,
		dir:      dir,
		vmName:   vmName,
		imageRef: imageRef,
		started:  time.Now(),
		sink:     sink,
	}
	if len(vmDir) > 0 {
		run.vmDir = vmDir[0]
	}
	if verbose {
		fmt.Printf("metrics: %s (%s)\n", id, dir)
	}
	return run, nil
}

func (r *standaloneMetricsRun) Dir() string {
	if r == nil {
		return ""
	}
	return r.dir
}

func finishStandaloneMetricsRun(run *standaloneMetricsRun) {
	if run == nil {
		return
	}
	run.EmitResourceSampleMetric("end")
	if err := run.sink.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: metrics close: %v\n", err)
	}
}

func newMetricsSink(path string) (runmetrics.Sink, error) {
	jsonl, err := runmetrics.NewJSONLSink(path)
	if err != nil {
		return nil, err
	}
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		return jsonl, nil
	}
	return runmetrics.MultiSink{jsonl, runmetrics.NewOTLPSink(endpoint)}, nil
}

func (b *RunBundle) EmitMetricEvent(eventType string, started time.Time, status string, extra map[string]any) {
	if b == nil {
		return
	}
	durationMS := int64(0)
	if !started.IsZero() {
		durationMS = time.Since(started).Milliseconds()
	}
	eventExtra := copyMetricExtra(extra)
	eventExtra["run_id"] = b.ID()
	if err := b.EmitMetric(context.Background(), runmetrics.Event{
		EventType:  eventType,
		VMName:     b.vmName,
		ImageRef:   b.forkFrom,
		DurationMS: durationMS,
		Status:     status,
		Extra:      eventExtra,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: metrics event %s: %v\n", eventType, err)
	}
}

func (run *standaloneMetricsRun) EmitMetricEvent(eventType string, started time.Time, status string, extra map[string]any) {
	if run == nil {
		return
	}
	durationMS := int64(0)
	if !started.IsZero() {
		durationMS = time.Since(started).Milliseconds()
	}
	eventExtra := copyMetricExtra(extra)
	eventExtra["run_id"] = run.id
	if err := run.sink.Emit(context.Background(), runmetrics.Event{
		EventType:  eventType,
		VMName:     run.vmName,
		ImageRef:   run.imageRef,
		DurationMS: durationMS,
		Status:     status,
		Extra:      eventExtra,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: metrics event %s: %v\n", eventType, err)
	}
}

func (b *RunBundle) MarkAgentReady() {
	if b == nil {
		return
	}
	b.mu.Lock()
	if b.metricAgentReady {
		b.mu.Unlock()
		return
	}
	b.metricAgentReady = true
	started := b.startedAt
	b.mu.Unlock()
	b.EmitMetricEvent("agent_ready", started, "ok", nil)
	b.EmitResourceSampleMetric("start")
}

func (run *standaloneMetricsRun) MarkAgentReady() {
	if run == nil {
		return
	}
	run.mu.Lock()
	if run.agentOK {
		run.mu.Unlock()
		return
	}
	run.agentOK = true
	started := run.started
	run.mu.Unlock()
	run.EmitMetricEvent("agent_ready", started, "ok", nil)
	run.EmitResourceSampleMetric("start")
}

func (b *RunBundle) EmitResourceSampleMetric(phase string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	vmDir := b.vmDir
	started := b.startedAt
	b.mu.Unlock()
	emitResourceSampleMetric(b, started, vmDir, phase, b.markResourceSampled)
}

func (run *standaloneMetricsRun) EmitResourceSampleMetric(phase string) {
	if run == nil {
		return
	}
	emitResourceSampleMetric(run, run.started, run.vmDir, phase, run.markResourceSampled)
}

func emitResourceSampleMetric(recorder interface {
	EmitMetricEvent(string, time.Time, string, map[string]any)
}, started time.Time, vmDir string, phase string, mark func(string) bool) {
	if recorder == nil || strings.TrimSpace(vmDir) == "" {
		return
	}
	extra := map[string]any{"phase": phase}
	added := addGuestResourceFields(vmDir, extra)
	added = addVZMemoryResourceFields(vmDir, extra) || added
	if !added {
		return
	}
	if mark != nil && !mark(phase) {
		return
	}
	recorder.EmitMetricEvent("resource_sample", started, "ok", extra)
}

func addGuestResourceFields(vmDir string, extra map[string]any) bool {
	info, err := resourceAgentInfoHook(vmDir)
	if err != nil || info == nil || strings.TrimSpace(info.RawJson) == "" {
		return false
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(info.RawJson), &raw); err != nil {
		return false
	}
	added := false
	total, okTotal := metricUint(raw, "memory_total", "memoryTotal")
	available, okAvailable := metricUint(raw, "memory_available", "memoryAvailable")
	if okTotal {
		extra["memory_total_bytes"] = total
		added = true
	}
	if okAvailable {
		extra["memory_available_bytes"] = available
		added = true
	}
	return added
}

func addVZMemoryResourceFields(vmDir string, extra map[string]any) bool {
	info, err := resourceMemoryInfoHook(vmDir)
	if err != nil || info == nil || info.GetInfo() == nil {
		return false
	}
	mem := info.GetInfo()
	if mem.GetConfiguredGb() > 0 {
		extra["configured_memory_gb"] = mem.GetConfiguredGb()
	}
	if mem.GetTargetGb() > 0 {
		extra["target_memory_gb"] = mem.GetTargetGb()
	}
	if mem.GetMinimumAllowedMb() > 0 {
		extra["minimum_allowed_memory_mb"] = mem.GetMinimumAllowedMb()
	}
	extra["has_balloon"] = mem.GetHasBalloon()
	return true
}

func (b *RunBundle) markResourceSampled(phase string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.resourceSampled == nil {
		b.resourceSampled = make(map[string]bool)
	}
	if b.resourceSampled[phase] {
		return false
	}
	b.resourceSampled[phase] = true
	return true
}

func (run *standaloneMetricsRun) markResourceSampled(phase string) bool {
	run.mu.Lock()
	defer run.mu.Unlock()
	if run.resourceSampled == nil {
		run.resourceSampled = make(map[string]bool)
	}
	if run.resourceSampled[phase] {
		return false
	}
	run.resourceSampled[phase] = true
	return true
}

func metricUint(raw map[string]any, names ...string) (uint64, bool) {
	for _, name := range names {
		switch v := raw[name].(type) {
		case float64:
			if v >= 0 {
				return uint64(v), true
			}
		case string:
			var n uint64
			if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
				return n, true
			}
		}
	}
	return 0, false
}

func (b *RunBundle) EmitCaptureLatency(ctx context.Context, e controlserver.CaptureLatencyEvent) {
	emitCaptureLatencyTo(ctx, b, e)
}

func (run *standaloneMetricsRun) EmitCaptureLatency(ctx context.Context, e controlserver.CaptureLatencyEvent) {
	emitCaptureLatencyTo(ctx, run, e)
}

func emitCaptureLatencyTo(ctx context.Context, recorder interface {
	EmitMetricEvent(string, time.Time, string, map[string]any)
}, e controlserver.CaptureLatencyEvent) {
	if recorder == nil {
		return
	}
	_ = ctx
	extra := map[string]any{
		"backend":           e.Backend,
		"requested_backend": e.RequestedBackend,
		"fallback":          e.Fallback,
	}
	if e.FallbackCause != "" {
		extra["fallback_cause"] = e.FallbackCause
	}
	if e.Width > 0 {
		extra["width"] = e.Width
	}
	if e.Height > 0 {
		extra["height"] = e.Height
	}
	if e.Error != "" {
		extra["error"] = e.Error
	}
	status := e.Status
	if status == "" {
		status = "ok"
	}
	started := time.Now()
	if e.DurationMS > 0 {
		started = started.Add(-time.Duration(e.DurationMS) * time.Millisecond)
	}
	recorder.EmitMetricEvent("capture_latency", started, status, extra)
}

func copyMetricExtra(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}
