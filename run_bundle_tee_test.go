package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type stubGetErrorResp struct{ err string }

func (s stubGetErrorResp) GetError() string { return s.err }

func TestTeeControlEventNoActiveBundleNoOp(t *testing.T) {
	if ActiveRunBundle() != nil {
		t.Skip("active run bundle leaked from another test")
	}
	teeControlEvent("agent-ping", stubGetErrorResp{})
}

func TestTeeControlEventAppendsEvent(t *testing.T) {
	tmp := t.TempDir()
	runsRoot := filepath.Join(tmp, "runs")
	b, err := NewRunBundle(runsRoot, "vm-x", "base")
	if err != nil {
		t.Fatalf("NewRunBundle: %v", err)
	}
	setActiveRunBundle(b)
	t.Cleanup(func() { setActiveRunBundle(nil) })

	teeControlEvent("snapshot-save", stubGetErrorResp{})
	teeControlEvent("disk-list", stubGetErrorResp{err: "boom"})

	data, err := os.ReadFile(filepath.Join(b.Dir(), "events.jsonl"))
	if err != nil {
		t.Fatalf("read events.jsonl: %v", err)
	}

	var events []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		var ev map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			t.Fatalf("unmarshal event %q: %v", scanner.Text(), err)
		}
		events = append(events, ev)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events, want 2", len(events))
	}
	if events[0]["req_type"] != "snapshot-save" || events[0]["event"] != "control" {
		t.Errorf("event 0 = %v, want req_type=snapshot-save event=control", events[0])
	}
	if _, hasErr := events[0]["error"]; hasErr {
		t.Errorf("event 0 should not have error: %v", events[0])
	}
	if events[1]["req_type"] != "disk-list" || events[1]["error"] != "boom" {
		t.Errorf("event 1 = %v, want req_type=disk-list error=boom", events[1])
	}
}

func TestTeeControlEventNilResp(t *testing.T) {
	tmp := t.TempDir()
	runsRoot := filepath.Join(tmp, "runs")
	b, err := NewRunBundle(runsRoot, "vm-y", "base")
	if err != nil {
		t.Fatalf("NewRunBundle: %v", err)
	}
	setActiveRunBundle(b)
	t.Cleanup(func() { setActiveRunBundle(nil) })

	teeControlEvent("vm-state", nil)

	data, err := os.ReadFile(filepath.Join(b.Dir(), "events.jsonl"))
	if err != nil {
		t.Fatalf("read events.jsonl: %v", err)
	}
	var ev map[string]any
	if err := json.Unmarshal(data[:len(data)-1], &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ev["req_type"] != "vm-state" || ev["event"] != "control" {
		t.Errorf("event = %v, want req_type=vm-state event=control", ev)
	}
	if _, hasErr := ev["error"]; hasErr {
		t.Errorf("nil-resp event should not have error: %v", ev)
	}
}
