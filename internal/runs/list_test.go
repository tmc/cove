package runs

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/tmc/vz-macos/internal/metrics"
)

func TestList(t *testing.T) {
	root := copyListFixtures(t)
	now := mustTime(t, "2026-05-05T12:00:00Z")

	tests := []struct {
		name   string
		filter Filter
		want   []string
	}{
		{
			name: "default newest first skips incomplete",
			want: []string{"run-fail", "run-ok-new", "run-ok-old"},
		},
		{
			name:   "limit",
			filter: Filter{Limit: 2},
			want:   []string{"run-fail", "run-ok-new"},
		},
		{
			name:   "since",
			filter: Filter{Since: 2 * time.Hour, Now: now},
			want:   []string{"run-fail", "run-ok-new"},
		},
		{
			name:   "status ok",
			filter: Filter{Status: "ok"},
			want:   []string{"run-ok-new", "run-ok-old"},
		},
		{
			name:   "status fail",
			filter: Filter{Status: "fail"},
			want:   []string{"run-fail"},
		},
		{
			name:   "status all",
			filter: Filter{Status: "all"},
			want:   []string{"run-fail", "run-ok-new", "run-ok-old"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := List(root, tt.filter)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if gotIDs := summaryIDs(got); !reflect.DeepEqual(gotIDs, tt.want) {
				t.Fatalf("run IDs = %v, want %v", gotIDs, tt.want)
			}
		})
	}
}

func TestListSummaryFields(t *testing.T) {
	root := copyListFixtures(t)
	got, err := List(root, Filter{Status: "fail"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("runs = %d, want 1", len(got))
	}
	summary := got[0]
	if summary.RunID != "run-fail" {
		t.Fatalf("RunID = %q, want run-fail", summary.RunID)
	}
	if summary.ImageRef != "registry.example/fail:latest" {
		t.Fatalf("ImageRef = %q", summary.ImageRef)
	}
	if summary.VMName != "fail-vm" {
		t.Fatalf("VMName = %q", summary.VMName)
	}
	if summary.Status != "signal: killed" {
		t.Fatalf("Status = %q", summary.Status)
	}
	if summary.TotalDurationMS != 5400 {
		t.Fatalf("TotalDurationMS = %d, want 5400", summary.TotalDurationMS)
	}
	if summary.ExitCode == nil || *summary.ExitCode != 137 {
		t.Fatalf("ExitCode = %v, want 137", summary.ExitCode)
	}
	if want := mustTime(t, "2026-05-05T11:30:00Z"); !summary.StartedAt.Equal(want) {
		t.Fatalf("StartedAt = %s, want %s", summary.StartedAt, want)
	}
}

func TestListMissingRoot(t *testing.T) {
	got, err := List(filepath.Join(t.TempDir(), "missing"), Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("runs = %d, want 0", len(got))
	}
}

func copyListFixtures(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatalf("ReadDir testdata: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || len(name) < len("list-") || name[:len("list-")] != "list-" {
			continue
		}
		src := filepath.Join("testdata", name, "metrics.jsonl")
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("read %s: %v", src, err)
		}
		dir := filepath.Join(root, name[len("list-"):])
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(dir, "metrics.jsonl"), data, 0644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}
	return root
}

func summaryIDs(summaries []Summary) []string {
	ids := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		ids = append(ids, summary.RunID)
	}
	return ids
}

func TestListEventCount(t *testing.T) {
	root := t.TempDir()
	writeRun(t, root, "20260510-cnt", []metrics.Event{
		event("vm_start", "ok", 10, nil),
		event("agent_ready", "ok", 5, nil),
		event("build_step", "ok", 50, nil),
		event(runCompleteEvent, "ok", 100, map[string]any{"exit_code": 0}),
	}, nil)

	got, err := List(root, Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].EventCount != 4 {
		t.Fatalf("EventCount = %d, want 4", got[0].EventCount)
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	got, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("parse time %q: %v", s, err)
	}
	return got
}

func TestListFailedEvents(t *testing.T) {
	root := t.TempDir()
	writeRun(t, root, "20260510-fe", []metrics.Event{
		event("vm_start", "ok", 10, nil),
		event("agent_step", "fail", 5, nil),
		event("build_step", "error", 50, nil),
		event(runCompleteEvent, "ok", 100, map[string]any{"exit_code": 0}),
	}, nil)

	got, err := List(root, Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].FailedEvents != 2 {
		t.Fatalf("FailedEvents = %d, want 2", got[0].FailedEvents)
	}
	if got[0].EventCount != 4 {
		t.Fatalf("EventCount = %d, want 4", got[0].EventCount)
	}
}
