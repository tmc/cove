package runs

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/metrics"
)

func TestExportJSON(t *testing.T) {
	root := t.TempDir()
	events := []metrics.Event{
		event("vm_start", "ok", 12, nil),
		event("run_complete", "ok", 34, map[string]any{"exit_code": 0}),
	}
	writeRun(t, root, "20260505-json", events, nil)

	var buf bytes.Buffer
	if err := ExportJSON(&buf, root, "20260505-j"); err != nil {
		t.Fatalf("ExportJSON: %v", err)
	}
	var got []metrics.Event
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(got) != 2 || got[0].EventType != "vm_start" || got[1].EventType != "run_complete" {
		t.Fatalf("events = %+v", got)
	}
}

func TestExportGHASummary(t *testing.T) {
	root := t.TempDir()
	writeRun(t, root, "20260505-gha", []metrics.Event{
		event("fork_created", "ok", 10, map[string]any{
			"source_kind": "vm",
			"source_ref":  "base-vm",
			"child_name":  "base-vm-job-1",
			"mode":        "linked-clone",
			"disk_reuse":  "apfs-copy-on-write",
			"ephemeral":   true,
			"keep":        false,
			"cleanup":     "remove-on-stop",
			"limitation":  "temporary RAM overlay disabled",
		}),
		event("network_policy", "ok", 12, map[string]any{
			"policy":        "packages",
			"mode":          "nat",
			"enforcement":   "not-hooked",
			"audit_log":     true,
			"allow_domains": []any{"pypi.org", "ghcr.io"},
			"limitation":    "nat records intent",
		}),
		event("vm_start", "failed", 20, map[string]any{"reason": "boot failed"}),
		event("run_complete", "failed", 30, map[string]any{"exit_code": 1}),
	}, nil)

	var buf bytes.Buffer
	if err := ExportGHASummary(&buf, root, "20260505-g"); err != nil {
		t.Fatalf("ExportGHASummary: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"## Cove Run 20260505-gha",
		"| Phase | Status | Duration |",
		"| fork_created | [ok] ok | 10ms |",
		"| vm_start | [fail] failed | 20ms |",
		"**Result:** [fail] failed exit_code=1 wallclock=30ms failed_events=2",
		"**Failure:** `vm_start`: boot failed",
		"### Fork",
		"| source | vm base-vm |",
		"| child | base-vm-job-1 |",
		"| mode | linked-clone |",
		"| disk_reuse | apfs-copy-on-write |",
		"| ephemeral | true |",
		"| keep | false |",
		"| cleanup | remove-on-stop |",
		"| limitation | temporary RAM overlay disabled |",
		"### Network",
		"| policy | packages mode=nat |",
		"| enforcement | not-hooked |",
		"| audit_log | true |",
		"| allow_domains | pypi.org, ghcr.io |",
		"| limitation | nat records intent |",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("summary missing %q:\n%s", want, out)
		}
	}
}

func TestExportGHASummaryIncludesImageRef(t *testing.T) {
	root := t.TempDir()
	start := event("vm_start", "ok", 10, nil)
	start.ImageRef = "ubuntu:24.04@sha256:abc"
	writeRun(t, root, "20260510-img", []metrics.Event{
		start,
		event("run_complete", "ok", 20, map[string]any{"exit_code": 0}),
	}, nil)

	var buf bytes.Buffer
	if err := ExportGHASummary(&buf, root, "20260510-img"); err != nil {
		t.Fatalf("ExportGHASummary: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "image_ref=ubuntu:24.04@sha256:abc") {
		t.Fatalf("summary missing image_ref:\n%s", out)
	}
}

func TestExportGHASummaryIncludesResourceSummary(t *testing.T) {
	root := t.TempDir()
	writeRun(t, root, "20260510-res", []metrics.Event{
		event("resource_sample", "ok", 10, map[string]any{
			"phase":                  "periodic",
			"memory_total_bytes":     1024,
			"memory_available_bytes": 64,
			"guest_load_avg_1":       4.5,
			"guest_top_processes": []any{
				map[string]any{"pid": 301, "cpu_percent": 81.25, "rss_bytes": 128, "command": "swift"},
			},
		}),
		event("run_complete", "failed", 20, map[string]any{"exit_code": 1}),
	}, nil)

	var buf bytes.Buffer
	if err := ExportGHASummary(&buf, root, "20260510-res"); err != nil {
		t.Fatalf("ExportGHASummary: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"### Resources",
		"| guest_memory_min_available | 64 B (6.2%) |",
		"| guest_top_process | swift pid=301 cpu=81.2% rss=128 B phase=periodic |",
		"**Resource hints:**",
		"`guest_memory_low`",
		"`guest_process_hot`",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("summary missing %q:\n%s", want, out)
		}
	}
}

func TestExportGHASummaryOmitsImageRefWhenAbsent(t *testing.T) {
	root := t.TempDir()
	writeRun(t, root, "20260510-noimg", []metrics.Event{
		event("run_complete", "ok", 5, map[string]any{"exit_code": 0}),
	}, nil)

	var buf bytes.Buffer
	if err := ExportGHASummary(&buf, root, "20260510-noimg"); err != nil {
		t.Fatalf("ExportGHASummary: %v", err)
	}
	if strings.Contains(buf.String(), "image_ref=") {
		t.Fatalf("summary should omit image_ref:\n%s", buf.String())
	}
}

func TestExportGHASummaryRendersEmptyStatusAsNA(t *testing.T) {
	root := t.TempDir()
	writeRun(t, root, "20260510-na", []metrics.Event{
		event("agent_ready", "", 7, nil),
		event("run_complete", "ok", 9, nil),
	}, nil)

	var buf bytes.Buffer
	if err := ExportGHASummary(&buf, root, "20260510-na"); err != nil {
		t.Fatalf("ExportGHASummary: %v", err)
	}
	if !strings.Contains(buf.String(), "| agent_ready | [n/a] n/a | 7ms |") {
		t.Fatalf("summary missing n/a badge:\n%s", buf.String())
	}
}

func TestExportTarGzContainsRunFiles(t *testing.T) {
	root := t.TempDir()
	writeRun(t, root, "20260505-tar", []metrics.Event{
		event("run_complete", "ok", 1, nil),
	}, map[string]string{
		"stdout.log":           "out\n",
		"screenshots/shot.txt": "shot\n",
	})

	var buf bytes.Buffer
	if err := ExportTarGz(&buf, root, "20260505-t"); err != nil {
		t.Fatalf("ExportTarGz: %v", err)
	}
	names := tarNames(t, buf.Bytes())
	for _, want := range []string{
		"20260505-tar/metrics.jsonl",
		"20260505-tar/stdout.log",
		"20260505-tar/screenshots/shot.txt",
	} {
		if !names[want] {
			t.Fatalf("tar missing %q in %#v", want, names)
		}
	}
}

func tarNames(t *testing.T, data []byte) map[string]bool {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	names := map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		names[h.Name] = true
	}
	return names
}
