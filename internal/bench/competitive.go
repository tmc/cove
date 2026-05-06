package bench

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tmc/vz-macos/internal/metrics"
)

// CompetitiveConfig configures the competitive benchmark report.
type CompetitiveConfig struct {
	RepoRoot     string
	OutPath      string
	MarkdownPath string
	RunsRoot     string
	Now          time.Time
}

// Report is the normalized competitive benchmark result.
type Report struct {
	GeneratedAt string   `json:"generated_at"`
	RunID       string   `json:"run_id"`
	GitHead     string   `json:"git_head"`
	OriginMain  string   `json:"origin_main"`
	Results     []Result `json:"results"`
}

// Result is one tool cell for one benchmark workload.
type Result struct {
	Workload    string `json:"workload"`
	Tool        string `json:"tool"`
	Status      string `json:"status"`
	ValueMS     *int64 `json:"value_ms,omitempty"`
	Unit        string `json:"unit,omitempty"`
	Source      string `json:"source,omitempty"`
	Methodology string `json:"methodology,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// RunCompetitive writes the normalized competitive benchmark report and emits a
// cove run artifact containing the same results as metrics events.
func RunCompetitive(ctx context.Context, cfg CompetitiveConfig) (Report, error) {
	root := cfg.RepoRoot
	if root == "" {
		root = "."
	}
	now := cfg.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	runID := "bench-" + now.UTC().Format("20060102-150405")
	report := Report{
		GeneratedAt: now.UTC().Format(time.RFC3339),
		RunID:       runID,
		GitHead:     gitValue(root, "HEAD"),
		OriginMain:  gitValue(root, "origin/main"),
	}

	var results []Result
	results = append(results, coveResults(root)...)
	results = append(results, competitorRows(root)...)
	report.Results = results

	if cfg.OutPath != "" {
		if err := writeJSON(cfg.OutPath, report); err != nil {
			return Report{}, err
		}
	}
	if cfg.MarkdownPath != "" {
		if err := writeMarkdown(cfg.MarkdownPath, report); err != nil {
			return Report{}, err
		}
	}
	if cfg.RunsRoot != "" {
		if err := writeRunMetrics(ctx, cfg.RunsRoot, report, cfg.OutPath); err != nil {
			return Report{}, err
		}
	}
	return report, nil
}

func coveResults(root string) []Result {
	return []Result{
		coveSummaryResult(root, "parallel_fork_16", "bench/parallel-fork/results-20260506-m4x-129/summary.md", "| 16 |", "parallel fork fan-out from a stopped 60 GiB macOS VM, 16 children"),
		coveJSONLResult(root, "boot_to_agent", "bench/boot-to-agent/results-20260506-m4x-129/runs.jsonl", "agent_ready_ms", "boot local image and wait for guest agent readiness"),
		coveJSONLResult(root, "image_build", "bench/image-build/results-20260506-m4x-129/runs.jsonl", "duration_ms", "snapshot a stopped VM into a local cove image"),
	}
}

func coveSummaryResult(root, workload, rel, rowPrefix, methodology string) Result {
	path := filepath.Join(root, filepath.FromSlash(rel))
	value, err := summaryTableMS(path, rowPrefix)
	if err != nil {
		return Result{Workload: workload, Tool: "cove", Status: "not_measured", Source: rel, Methodology: methodology, Reason: err.Error()}
	}
	return Result{Workload: workload, Tool: "cove", Status: "ok", ValueMS: &value, Unit: "ms", Source: rel, Methodology: methodology}
}

func coveJSONLResult(root, workload, rel, field, methodology string) Result {
	path := filepath.Join(root, filepath.FromSlash(rel))
	value, err := firstJSONLMS(path, field)
	if err != nil {
		return Result{Workload: workload, Tool: "cove", Status: "not_measured", Source: rel, Methodology: methodology, Reason: err.Error()}
	}
	return Result{Workload: workload, Tool: "cove", Status: "ok", ValueMS: &value, Unit: "ms", Source: rel, Methodology: methodology}
}

func competitorRows(root string) []Result {
	workloads := []string{"parallel_fork_16", "boot_to_agent", "image_build"}
	tools := []struct {
		name   string
		bin    string
		reason string
	}{
		{"lume", "lume", "Lume benchmark protocol exists, but no same-host Lume VM/image was provided for this run"},
		{"docker_mac", "docker", "Docker Desktop was not run for these VM workloads; container startup is not equivalent to macOS/Linux VM fork, boot-to-agent, or image-build timing"},
		{"cirrus", "cirrus", "Cirrus CI benchmark data was not run in this repository; do not fabricate hosted numbers"},
	}
	var out []Result
	for _, workload := range workloads {
		for _, tool := range tools {
			reason := tool.reason
			if _, err := exec.LookPath(tool.bin); err != nil {
				reason = tool.bin + " CLI not found on PATH"
			}
			out = append(out, Result{
				Workload:    workload,
				Tool:        tool.name,
				Status:      "not_measured",
				Methodology: competitorMethodology(root, tool.name),
				Reason:      reason,
			})
		}
	}
	return out
}

func competitorMethodology(root, tool string) string {
	switch tool {
	case "lume":
		if exists(filepath.Join(root, "bench/cove-vs-lume/run.sh")) {
			return "bench/cove-vs-lume/run.sh"
		}
	case "cirrus":
		if exists(filepath.Join(root, "bench/cove-vs-cirrus/run.sh")) {
			return "bench/cove-vs-cirrus/run.sh"
		}
	}
	return "not run"
}

func summaryTableMS(path, rowPrefix string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		line := strings.TrimSpace(scan.Text())
		if !strings.HasPrefix(line, rowPrefix) {
			continue
		}
		fields := strings.Split(line, "|")
		for _, field := range fields {
			field = strings.TrimSpace(field)
			if strings.HasSuffix(field, "ms") {
				return strconv.ParseInt(strings.TrimSuffix(field, "ms"), 10, 64)
			}
		}
	}
	if err := scan.Err(); err != nil {
		return 0, err
	}
	return 0, fmt.Errorf("row %q not found", rowPrefix)
}

func firstJSONLMS(path, field string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		var row map[string]any
		if err := json.Unmarshal(scan.Bytes(), &row); err != nil {
			return 0, err
		}
		if row["status"] != "ok" {
			continue
		}
		if v, ok := row[field].(float64); ok {
			return int64(v), nil
		}
	}
	if err := scan.Err(); err != nil {
		return 0, err
	}
	return 0, fmt.Errorf("field %q not found", field)
}

func writeJSON(path string, report Report) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("bench report: create dir: %w", err)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("bench report: marshal json: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("bench report: write json: %w", err)
	}
	return nil
}

func writeMarkdown(path string, report Report) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("bench report: create markdown dir: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Competitive benchmark results, May 2026\n\n")
	fmt.Fprintf(&b, "- Generated: `%s`\n", report.GeneratedAt)
	fmt.Fprintf(&b, "- Run id: `%s`\n", report.RunID)
	fmt.Fprintf(&b, "- Git HEAD: `%s`\n", report.GitHead)
	fmt.Fprintf(&b, "- Origin main: `%s`\n\n", report.OriginMain)
	fmt.Fprintf(&b, "| Workload | cove | Lume | Docker-Mac | Cirrus | Evidence |\n")
	fmt.Fprintf(&b, "|---|---:|---:|---:|---:|---|\n")
	for _, workload := range workloads(report.Results) {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n",
			workload,
			cell(report.Results, workload, "cove"),
			cell(report.Results, workload, "lume"),
			cell(report.Results, workload, "docker_mac"),
			cell(report.Results, workload, "cirrus"),
			sourceCell(report.Results, workload),
		)
	}
	fmt.Fprintf(&b, "\nCompetitor cells remain `not measured` unless this repository has a same-host run for that tool and workload. Cirrus hosted benchmark numbers are not fabricated.\n")
	if err := os.WriteFile(path, []byte(b.String()), 0644); err != nil {
		return fmt.Errorf("bench report: write markdown: %w", err)
	}
	return nil
}

func writeRunMetrics(ctx context.Context, root string, report Report, outPath string) error {
	dir := filepath.Join(root, report.RunID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("bench metrics: create run dir: %w", err)
	}
	sink, err := metrics.NewJSONLSink(filepath.Join(dir, "metrics.jsonl"))
	if err != nil {
		return err
	}
	defer sink.Close()
	start := time.Now()
	for _, result := range report.Results {
		extra := map[string]any{
			"run_id":      report.RunID,
			"workload":    result.Workload,
			"tool":        result.Tool,
			"source":      result.Source,
			"methodology": result.Methodology,
		}
		if result.Reason != "" {
			extra["reason"] = result.Reason
		}
		if err := sink.Emit(ctx, metrics.Event{
			EventType:  "benchmark_result",
			DurationMS: valueMS(result.ValueMS),
			Status:     result.Status,
			Extra:      extra,
		}); err != nil {
			return err
		}
	}
	return sink.Emit(ctx, metrics.Event{
		EventType:  "run_complete",
		DurationMS: time.Since(start).Milliseconds(),
		Status:     "ok",
		Extra: map[string]any{
			"run_id":      report.RunID,
			"command":     "bench competitive",
			"result_path": outPath,
		},
	})
}

func workloads(results []Result) []string {
	seen := map[string]bool{}
	var out []string
	for _, result := range results {
		if seen[result.Workload] {
			continue
		}
		seen[result.Workload] = true
		out = append(out, result.Workload)
	}
	sort.Strings(out)
	return out
}

func cell(results []Result, workload, tool string) string {
	for _, result := range results {
		if result.Workload != workload || result.Tool != tool {
			continue
		}
		if result.Status == "ok" && result.ValueMS != nil {
			return fmt.Sprintf("%dms", *result.ValueMS)
		}
		if result.Reason != "" {
			return "not measured"
		}
		return result.Status
	}
	return "not measured"
}

func sourceCell(results []Result, workload string) string {
	for _, result := range results {
		if result.Workload == workload && result.Tool == "cove" && result.Source != "" {
			return "`" + result.Source + "`"
		}
	}
	return ""
}

func valueMS(v *int64) int64 {
	if v == nil {
		return 0
	}
	return *v
}

func gitValue(root, rev string) string {
	cmd := exec.Command("git", "rev-parse", rev)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
