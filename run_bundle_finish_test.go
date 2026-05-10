package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFinishRunBundleNilClearsActive(t *testing.T) {
	prev := ActiveRunBundle()
	t.Cleanup(func() { setActiveRunBundle(prev) })
	b, err := NewRunBundle(t.TempDir(), "vm", "")
	if err != nil {
		t.Fatalf("NewRunBundle: %v", err)
	}
	setActiveRunBundle(b)
	finishRunBundle(nil, nil)
	if ActiveRunBundle() != nil {
		t.Fatal("ActiveRunBundle != nil after finishRunBundle(nil, nil)")
	}
}

func TestFinishRunBundleWritesExitEventAndFinalizes(t *testing.T) {
	prev := ActiveRunBundle()
	t.Cleanup(func() { setActiveRunBundle(prev) })
	dir := t.TempDir()
	b, err := NewRunBundle(dir, "vm", "")
	if err != nil {
		t.Fatalf("NewRunBundle: %v", err)
	}
	setActiveRunBundle(b)

	finishRunBundle(b, errors.New("boom"))

	if ActiveRunBundle() != nil {
		t.Fatal("ActiveRunBundle != nil after finishRunBundle")
	}
	data, err := os.ReadFile(filepath.Join(b.Dir(), "events.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile events: %v", err)
	}
	if !strings.Contains(string(data), `"run.exit"`) {
		t.Fatalf("events = %q, want run.exit", data)
	}
	if !strings.Contains(string(data), "boom") {
		t.Fatalf("events = %q, want exit error", data)
	}
}
