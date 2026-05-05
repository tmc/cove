package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	runmetrics "github.com/tmc/vz-macos/internal/metrics"
)

type standaloneMetricsRun struct {
	id       string
	dir      string
	vmName   string
	imageRef string
	started  time.Time
	sink     runmetrics.Sink
	agentOK  bool
}

var (
	activeMetricsMu  sync.Mutex
	activeMetricsRun *standaloneMetricsRun
)

func beginStandaloneMetricsRun(vmName, imageRef string) (*standaloneMetricsRun, error) {
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
	activeMetricsMu.Lock()
	activeMetricsRun = run
	activeMetricsMu.Unlock()
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
	if err := run.sink.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: metrics close: %v\n", err)
	}
	activeMetricsMu.Lock()
	if activeMetricsRun == run {
		activeMetricsRun = nil
	}
	activeMetricsMu.Unlock()
}

func activeStandaloneMetricsRun() *standaloneMetricsRun {
	activeMetricsMu.Lock()
	defer activeMetricsMu.Unlock()
	return activeMetricsRun
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

func emitMetricEvent(eventType string, started time.Time, status string, extra map[string]any) {
	durationMS := int64(0)
	if !started.IsZero() {
		durationMS = time.Since(started).Milliseconds()
	}
	if b := ActiveRunBundle(); b != nil {
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
		return
	}
	run := activeStandaloneMetricsRun()
	if run == nil {
		return
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

func emitAgentReadyMetric() {
	if b := ActiveRunBundle(); b != nil {
		b.mu.Lock()
		if b.metricAgentReady {
			b.mu.Unlock()
			return
		}
		b.metricAgentReady = true
		started := b.startedAt
		b.mu.Unlock()
		emitMetricEvent("agent_ready", started, "ok", nil)
		return
	}
	run := activeStandaloneMetricsRun()
	if run == nil {
		return
	}
	activeMetricsMu.Lock()
	if run.agentOK {
		activeMetricsMu.Unlock()
		return
	}
	run.agentOK = true
	started := run.started
	activeMetricsMu.Unlock()
	emitMetricEvent("agent_ready", started, "ok", nil)
}

func copyMetricExtra(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}
