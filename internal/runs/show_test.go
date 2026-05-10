package runs

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/vz-macos/internal/metrics"
)

func TestLoadShowPrefixAmbiguous(t *testing.T) {
	root := t.TempDir()
	writeRun(t, root, "20260505-aaa111", nil, nil)
	writeRun(t, root, "20260505-aab222", nil, nil)

	_, err := LoadShow(root, "20260505-aa")
	if err == nil {
		t.Fatal("LoadShow returned nil error")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("error = %q, want ambiguous", err)
	}
}

func TestLoadShowPrefixNotFound(t *testing.T) {
	root := t.TempDir()

	_, err := LoadShow(root, "missing")
	if err == nil {
		t.Fatal("LoadShow returned nil error")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("error = %q, want not found", err)
	}
}

func TestRenderShow(t *testing.T) {
	root := t.TempDir()
	events := []metrics.Event{
		event("fork_created", "ok", 10, nil),
		event("vm_create", "ok", 20, nil),
		event("vm_start", "error", 30, map[string]any{"reason": "boot failed\nfull detail"}),
		event("agent_ready", "ok", 40, nil),
		event("build_step", "ok", 50, nil),
		event("run_complete", "failed", 150, map[string]any{"exit_code": 2}),
	}
	writeRun(t, root, "20260505-show", events, map[string]string{
		"stdout.log":          "hello\n",
		"screenshots/one.txt": "shot\n",
	})

	show, err := LoadShow(root, "20260505-show")
	if err != nil {
		t.Fatalf("LoadShow: %v", err)
	}
	if len(show.Events) != len(events) {
		t.Fatalf("events = %d, want %d", len(show.Events), len(events))
	}
	if show.Result.Status != "failed" || !show.Result.HasExitCode || show.Result.ExitCode != 2 || show.Result.WallclockMS != 150 {
		t.Fatalf("result = %+v", show.Result)
	}
	if show.Failure.Class != "vm_start" || show.Failure.Reason != "boot failed" {
		t.Fatalf("failure = %+v", show.Failure)
	}

	var buf bytes.Buffer
	if err := RenderShow(&buf, show); err != nil {
		t.Fatalf("RenderShow: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"Run: 20260505-show",
		"fork_created  ok  10ms",
		"Result: failed exit_code=2 wallclock=150ms",
		"Failure: vm_start: boot failed",
		"metrics.jsonl",
		"screenshots/one.txt",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func writeRun(t *testing.T, root, id string, events []metrics.Event, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	var lines []string
	for _, e := range events {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		lines = append(lines, string(b))
	}
	data := ""
	if len(lines) > 0 {
		data = strings.Join(lines, "\n") + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "metrics.jsonl"), []byte(data), 0644); err != nil {
		t.Fatalf("write metrics: %v", err)
	}
	for name, data := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(path, []byte(data), 0644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	return dir
}

func event(typ, status string, duration int64, extra map[string]any) metrics.Event {
	return metrics.Event{
		Timestamp:  "2026-05-05T12:00:00Z",
		EventType:  typ,
		DurationMS: duration,
		Status:     status,
		Extra:      extra,
	}
}

func TestLoadShowSumsArtifactBytes(t *testing.T) {
	root := t.TempDir()
	writeRun(t, root, "20260510-ab", []metrics.Event{
		event(runCompleteEvent, "ok", 1, nil),
	}, map[string]string{
		"stdout.log":   "abcdef\n",      // 7 bytes
		"sub/note.txt": "hello world\n", // 12 bytes
	})

	show, err := LoadShow(root, "20260510-ab")
	if err != nil {
		t.Fatalf("LoadShow: %v", err)
	}
	// metrics.jsonl from writeRun also counts; the two seeded files
	// together are 19 bytes, so total must be at least 19.
	if show.ArtifactBytes < 19 {
		t.Fatalf("ArtifactBytes = %d, want >= 19", show.ArtifactBytes)
	}
}

func TestResultCountsFailedEvents(t *testing.T) {
	root := t.TempDir()
	writeRun(t, root, "20260510-fe", []metrics.Event{
		event("vm_start", "ok", 10, nil),
		event("build_step", "failed", 20, map[string]any{"reason": "compile"}),
		event("build_step", "failed", 5, map[string]any{"reason": "link"}),
		event(runCompleteEvent, "failed", 100, map[string]any{"exit_code": 1}),
	}, nil)

	show, err := LoadShow(root, "20260510-fe")
	if err != nil {
		t.Fatalf("LoadShow: %v", err)
	}
	if show.Result.FailedEvents != 3 {
		t.Fatalf("FailedEvents = %d, want 3", show.Result.FailedEvents)
	}
}
