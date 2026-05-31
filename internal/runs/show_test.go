package runs

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/metrics"
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

func TestLoadShowRejectsEmptyRootOrPrefix(t *testing.T) {
	tests := []struct {
		name   string
		root   string
		prefix string
		want   string
	}{
		{"root", "", "run", "runs root is empty"},
		{"prefix", t.TempDir(), "", "run prefix is empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadShow(tt.root, tt.prefix)
			if err == nil {
				t.Fatal("LoadShow returned nil error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want %q", err, tt.want)
			}
		})
	}
}

func TestLoadShowReportsMissingMetrics(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "20260510-nometrics"), 0755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	_, err := LoadShow(root, "20260510-nometrics")
	if err == nil {
		t.Fatal("LoadShow returned nil error")
	}
	if !strings.Contains(err.Error(), "open metrics:") {
		t.Fatalf("error = %q, want open metrics", err)
	}
}

func TestRenderShow(t *testing.T) {
	root := t.TempDir()
	events := []metrics.Event{
		event("fork_created", "ok", 10, map[string]any{
			"source_kind":  "image",
			"source_ref":   "macos-ci:latest",
			"child_name":   "macos-ci-run-1",
			"child_path":   "/tmp/macos-ci-run-1",
			"mode":         "image-materialized",
			"disk_reuse":   "clonefile",
			"ephemeral":    true,
			"keep":         false,
			"cleanup":      "remove-on-stop",
			"verification": "PASS",
		}),
		event("vm_create", "ok", 20, nil),
		event("network_policy", "ok", 22, map[string]any{
			"policy":        "egress",
			"mode":          "nat",
			"enforcement":   "not-hooked",
			"audit_log":     true,
			"allow_domains": []any{"api.openai.com", "ghcr.io"},
			"allow_cidrs":   []any{"10.0.0.0/8"},
			"limitation":    "nat records intent",
		}),
		event("resource_sample", "ok", 25, map[string]any{
			"phase":                  "periodic",
			"memory_total_bytes":     8 * 1024 * 1024 * 1024,
			"memory_available_bytes": 512 * 1024 * 1024,
			"guest_load_avg_1":       5.25,
			"guest_process_count":    42,
			"guest_top_processes": []any{
				map[string]any{"pid": 301, "cpu_percent": 91.5, "rss_bytes": 256 * 1024 * 1024, "command": "xcodebuild"},
			},
			"host_cpu_percent": 27.5,
			"host_rss_bytes":   2 * 1024 * 1024 * 1024,
		}),
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
	if show.Fork == nil || show.Fork.SourceKind != "image" || show.Fork.SourceRef != "macos-ci:latest" || show.Fork.ChildName != "macos-ci-run-1" || show.Fork.Ephemeral == nil || !*show.Fork.Ephemeral {
		t.Fatalf("fork = %+v", show.Fork)
	}
	if show.Resource == nil || show.Resource.SampleCount != 1 || show.Resource.TopGuestProcess == nil || show.Resource.TopGuestProcess.Command != "xcodebuild" {
		t.Fatalf("resource = %+v", show.Resource)
	}
	if show.Network == nil || show.Network.Policy != "egress" || show.Network.Mode != "nat" || !show.Network.AuditLog || len(show.Network.AllowDomains) != 2 {
		t.Fatalf("network = %+v", show.Network)
	}

	var buf bytes.Buffer
	if err := RenderShow(&buf, show); err != nil {
		t.Fatalf("RenderShow: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"Run: 20260505-show",
		"fork_created  ok  10ms",
		"Result: failed exit_code=2 wallclock=150ms failed_events=2",
		"Failure: vm_start: boot failed",
		"Fork:",
		"source: image macos-ci:latest",
		"child: macos-ci-run-1",
		"child_path: /tmp/macos-ci-run-1",
		"mode: image-materialized",
		"disk_reuse: clonefile",
		"ephemeral: true",
		"keep: false",
		"cleanup: remove-on-stop",
		"verification: PASS",
		"duration: 10ms",
		"Network:",
		"policy: egress mode=nat",
		"enforcement: not-hooked",
		"audit_log: true",
		"allow_domains: api.openai.com, ghcr.io",
		"allow_cidrs: 10.0.0.0/8",
		"limitation: nat records intent",
		"Resources:",
		"guest_memory_min_available: 512.0 MB (6.2%)",
		"guest_load_avg_1_peak: 5.25",
		"guest_top_process: xcodebuild pid=301 cpu=91.5% rss=256.0 MB phase=periodic",
		"guest_memory_low",
		"guest_process_hot",
		"Artifacts (",
		" bytes):",
		"metrics.jsonl",
		"screenshots/one.txt",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestLoadShowResourceSummaryCombinesSamples(t *testing.T) {
	root := t.TempDir()
	writeRun(t, root, "20260510-res", []metrics.Event{
		event("resource_sample", "ok", 10, map[string]any{
			"phase":                  "start",
			"memory_total_bytes":     "4096",
			"memory_available_bytes": "2048",
			"guest_load_avg_1":       "1.5",
			"guest_process_count":    "5",
			"guest_top_processes": []any{
				map[string]any{"pid": 11, "cpuPercent": "7.5", "rssBytes": "1024", "command": "sh"},
			},
			"host_cpu_percent": 3.5,
			"host_rss_bytes":   8192,
		}),
		event("resource_sample", "ok", 20, map[string]any{
			"phase":                  "end",
			"memory_total_bytes":     4096,
			"memory_available_bytes": 512,
			"guest_load_avg_1":       2.75,
			"guest_process_count":    9,
			"guest_top_processes": []map[string]any{
				{"pid": 12, "cpu_percent": 9.5, "rss_bytes": 2048, "command": "make"},
			},
			"host_cpu_percent": 4.5,
			"host_rss_bytes":   16384,
		}),
		event(runCompleteEvent, "ok", 30, nil),
	}, nil)

	show, err := LoadShow(root, "20260510-res")
	if err != nil {
		t.Fatalf("LoadShow: %v", err)
	}
	s := show.Resource
	if s == nil {
		t.Fatal("Resource = nil")
	}
	if s.SampleCount != 2 {
		t.Fatalf("SampleCount = %d, want 2", s.SampleCount)
	}
	if s.MinGuestMemoryAvailableBytes == nil || *s.MinGuestMemoryAvailableBytes != 512 {
		t.Fatalf("MinGuestMemoryAvailableBytes = %v", s.MinGuestMemoryAvailableBytes)
	}
	if s.PeakGuestLoadAvg1 == nil || *s.PeakGuestLoadAvg1 != 2.75 {
		t.Fatalf("PeakGuestLoadAvg1 = %v", s.PeakGuestLoadAvg1)
	}
	if s.PeakGuestProcessCount == nil || *s.PeakGuestProcessCount != 9 {
		t.Fatalf("PeakGuestProcessCount = %v", s.PeakGuestProcessCount)
	}
	if s.TopGuestProcess == nil || s.TopGuestProcess.Command != "make" || s.TopGuestProcess.Phase != "end" {
		t.Fatalf("TopGuestProcess = %+v", s.TopGuestProcess)
	}
	if s.PeakHostCPUPercent == nil || *s.PeakHostCPUPercent != 4.5 {
		t.Fatalf("PeakHostCPUPercent = %v", s.PeakHostCPUPercent)
	}
	if s.PeakHostRSSBytes == nil || *s.PeakHostRSSBytes != 16384 {
		t.Fatalf("PeakHostRSSBytes = %v", s.PeakHostRSSBytes)
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

func TestLoadShowFailureReasonFallbacks(t *testing.T) {
	tests := []struct {
		name  string
		extra map[string]any
		want  string
	}{
		{"reason", map[string]any{"reason": "boot failed"}, "boot failed"},
		{"error", map[string]any{"error": "agent failed"}, "agent failed"},
		{"status", nil, "failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeRun(t, root, "20260510-"+tt.name, []metrics.Event{
				event("build_step", "failed", 20, tt.extra),
				event(runCompleteEvent, "failed", 30, nil),
			}, nil)

			show, err := LoadShow(root, "20260510-"+tt.name)
			if err != nil {
				t.Fatalf("LoadShow: %v", err)
			}
			if show.Failure.Class != "build_step" || show.Failure.Reason != tt.want {
				t.Fatalf("Failure = %+v, want build_step %q", show.Failure, tt.want)
			}
		})
	}
}
