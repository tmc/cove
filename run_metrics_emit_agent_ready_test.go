package main

import "testing"

func TestEmitAgentReadyMetricNoActiveIsNoOp(t *testing.T) {
	prev := ActiveRunBundle()
	t.Cleanup(func() { setActiveRunBundle(prev) })
	setActiveRunBundle(nil)
	emitAgentReadyMetric()
}

func TestEmitAgentReadyMetricBundleSetsFlagOnce(t *testing.T) {
	prev := ActiveRunBundle()
	t.Cleanup(func() { setActiveRunBundle(prev) })

	b, err := NewRunBundle(t.TempDir(), "vm", "")
	if err != nil {
		t.Fatalf("NewRunBundle: %v", err)
	}
	setActiveRunBundle(b)
	if b.metricAgentReady {
		t.Fatal("metricAgentReady true before emit")
	}
	emitAgentReadyMetric()
	if !b.metricAgentReady {
		t.Fatal("metricAgentReady not set after first emit")
	}
	emitAgentReadyMetric() // idempotent re-entry
	if !b.metricAgentReady {
		t.Fatal("metricAgentReady cleared by second emit")
	}
}
