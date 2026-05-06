package bench

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSummaryTableMS(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "summary.md")
	data := "| Level | Status | Total wall |\n|---:|---|---:|\n| 16 | ok | 1163ms |\n"
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := summaryTableMS(path, "| 16 |")
	if err != nil {
		t.Fatal(err)
	}
	if got != 1163 {
		t.Fatalf("duration = %d, want 1163", got)
	}
}

func TestFirstJSONLMS(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runs.jsonl")
	data := `{"status":"skip","duration_ms":1}` + "\n" + `{"status":"ok","duration_ms":37648}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := firstJSONLMS(path, "duration_ms")
	if err != nil {
		t.Fatal(err)
	}
	if got != 37648 {
		t.Fatalf("duration = %d, want 37648", got)
	}
}

func TestRunCompetitiveWritesJSONAndMetrics(t *testing.T) {
	root := t.TempDir()
	write(t, root, "bench/parallel-fork/results-20260506-m4x-129/summary.md", "| 16 | ok | 1163ms |\n")
	write(t, root, "bench/boot-to-agent/results-20260506-m4x-129/runs.jsonl", `{"status":"ok","agent_ready_ms":52693}`+"\n")
	write(t, root, "bench/image-build/results-20260506-m4x-129/runs.jsonl", `{"status":"ok","duration_ms":37648}`+"\n")
	write(t, root, "bench/cove-vs-lume/run.sh", "#!/bin/sh\n")
	write(t, root, "bench/cove-vs-cirrus/run.sh", "#!/bin/sh\n")
	if err := os.WriteFile(filepath.Join(root, ".git"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "docs/benchmarks/results.json")
	runsRoot := filepath.Join(root, "runs")
	report, err := RunCompetitive(context.Background(), CompetitiveConfig{
		RepoRoot: root,
		OutPath:  out,
		RunsRoot: runsRoot,
		Now:      time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.RunID != "bench-20260506-120000" {
		t.Fatalf("run id = %q", report.RunID)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Report
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Results) != 12 {
		t.Fatalf("results = %d, want 12", len(decoded.Results))
	}
	if _, err := os.Stat(filepath.Join(runsRoot, report.RunID, "metrics.jsonl")); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, root, rel, data string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
}
