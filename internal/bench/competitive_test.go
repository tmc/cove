package bench

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
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

func TestSummaryTableMSErrors(t *testing.T) {
	dir := t.TempDir()
	badNumber := filepath.Join(dir, "bad.md")
	noRow := filepath.Join(dir, "missing.md")
	write(t, dir, "bad.md", "| 16 | ok | fastms |\n")
	write(t, dir, "missing.md", "| 8 | ok | 10ms |\n")

	for _, tc := range []struct {
		name    string
		path    string
		wantErr error
		want    string
	}{
		{name: "missing file", path: filepath.Join(dir, "gone.md"), wantErr: os.ErrNotExist},
		{name: "bad number", path: badNumber, want: "invalid syntax"},
		{name: "missing row", path: noRow, want: `row "| 16 |" not found`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := summaryTableMS(tc.path, "| 16 |")
			if err == nil {
				t.Fatal("summaryTableMS succeeded, want error")
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want errors.Is(..., %v)", err, tc.wantErr)
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

func TestFirstJSONLMSErrors(t *testing.T) {
	dir := t.TempDir()
	badJSON := filepath.Join(dir, "bad.jsonl")
	noField := filepath.Join(dir, "missing.jsonl")
	write(t, dir, "bad.jsonl", "{\n")
	write(t, dir, "missing.jsonl", `{"status":"ok","other_ms":1}`+"\n")

	for _, tc := range []struct {
		name    string
		path    string
		wantErr error
		want    string
	}{
		{name: "missing file", path: filepath.Join(dir, "gone.jsonl"), wantErr: os.ErrNotExist},
		{name: "bad json", path: badJSON, want: "unexpected end of JSON input"},
		{name: "missing field", path: noField, want: `field "duration_ms" not found`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := firstJSONLMS(tc.path, "duration_ms")
			if err == nil {
				t.Fatal("firstJSONLMS succeeded, want error")
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want errors.Is(..., %v)", err, tc.wantErr)
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tc.want)
			}
		})
	}
}

func TestWriteJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "reports", "bench.json")
	if err := writeJSON(path, Report{RunID: "bench-test"}); err != nil {
		t.Fatalf("writeJSON: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\"run_id\": \"bench-test\"") {
		t.Fatalf("json missing run id:\n%s", data)
	}

	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0644); err != nil {
		t.Fatal(err)
	}
	err = writeJSON(filepath.Join(blocker, "bench.json"), Report{})
	if !errors.Is(err, syscall.ENOTDIR) {
		t.Fatalf("writeJSON error = %v, want ENOTDIR", err)
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

func TestMarkdownCells(t *testing.T) {
	ok := int64(1234)
	results := []Result{
		{Workload: "image_build", Tool: "cove", Status: "ok", ValueMS: &ok, Source: "bench/image/runs.jsonl"},
		{Workload: "image_build", Tool: "lume", Status: "not_measured", Reason: "missing"},
		{Workload: "boot_to_agent", Tool: "cove", Status: "not_measured"},
	}
	for _, tc := range []struct {
		name string
		got  string
		want string
	}{
		{name: "ok value", got: cell(results, "image_build", "cove"), want: "1234ms"},
		{name: "reason hidden", got: cell(results, "image_build", "lume"), want: "not measured"},
		{name: "status fallback", got: cell(results, "boot_to_agent", "cove"), want: "not_measured"},
		{name: "missing tool", got: cell(results, "image_build", "cirrus"), want: "not measured"},
		{name: "source", got: sourceCell(results, "image_build"), want: "`bench/image/runs.jsonl`"},
		{name: "missing source", got: sourceCell(results, "boot_to_agent"), want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
}

func TestWriteMarkdown(t *testing.T) {
	value := int64(77)
	path := filepath.Join(t.TempDir(), "reports", "bench.md")
	report := Report{
		GeneratedAt: "2026-05-06T12:00:00Z",
		RunID:       "bench-20260506-120000",
		GitHead:     "head",
		OriginMain:  "main",
		Results: []Result{
			{Workload: "z_work", Tool: "cove", Status: "ok", ValueMS: &value, Source: "z.jsonl"},
			{Workload: "a_work", Tool: "cove", Status: "not_measured", Reason: "missing"},
		},
	}
	if err := writeMarkdown(path, report); err != nil {
		t.Fatalf("writeMarkdown: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{
		"- Run id: `bench-20260506-120000`",
		"| a_work | not measured | not measured | not measured | not measured |  |",
		"| z_work | 77ms | not measured | not measured | not measured | `z.jsonl` |",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("markdown missing %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "| a_work |") > strings.Index(got, "| z_work |") {
		t.Fatalf("workloads not sorted:\n%s", got)
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
