package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFinishRunBundleNilNoOp(t *testing.T) {
	finishRunBundle(nil, nil)
}

func TestFinishRunBundleWritesExitEventAndFinalizes(t *testing.T) {
	dir := t.TempDir()
	b, err := NewRunBundle(dir, "vm", "")
	if err != nil {
		t.Fatalf("NewRunBundle: %v", err)
	}

	finishRunBundle(b, errors.New("boom"))

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
