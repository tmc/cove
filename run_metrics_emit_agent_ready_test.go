package main

import "testing"

func TestMarkAgentReadyNilIsNoOp(t *testing.T) {
	var b *RunBundle
	b.MarkAgentReady()
}

func TestMarkAgentReadyBundleSetsFlagOnce(t *testing.T) {
	b, err := NewRunBundle(t.TempDir(), "vm", "")
	if err != nil {
		t.Fatalf("NewRunBundle: %v", err)
	}
	if b.metricAgentReady {
		t.Fatal("metricAgentReady true before emit")
	}
	b.MarkAgentReady()
	if !b.metricAgentReady {
		t.Fatal("metricAgentReady not set after first emit")
	}
	b.MarkAgentReady() // idempotent re-entry
	if !b.metricAgentReady {
		t.Fatal("metricAgentReady cleared by second emit")
	}
}
