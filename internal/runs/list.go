package runs

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/tmc/vz-macos/internal/metrics"
)

const runCompleteEvent = "run_complete"

// Summary describes one completed run.
type Summary struct {
	RunID           string    `json:"run_id"`
	ImageRef        string    `json:"image_ref,omitempty"`
	VMName          string    `json:"vm_name,omitempty"`
	Status          string    `json:"status"`
	TotalDurationMS int64     `json:"total_duration_ms"`
	ExitCode        *int      `json:"exit_code,omitempty"`
	StartedAt       time.Time `json:"started_at"`
	EventCount      int       `json:"event_count,omitempty"`
	FailedEvents    int       `json:"failed_events,omitempty"`
}

// Filter limits List results. Status is "ok", "fail", or "all".
type Filter struct {
	Limit  int
	Since  time.Duration
	Status string
	Now    time.Time
}

// List returns completed runs under root, newest started_at first.
func List(root string, filter Filter) ([]Summary, error) {
	if root == "" {
		return nil, fmt.Errorf("runs list: empty root")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("runs list: read root: %w", err)
	}

	var summaries []Summary
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		summary, ok, err := readRun(filepath.Join(root, entry.Name()), entry.Name())
		if err != nil {
			slog.Warn("skip malformed run metrics", "run", entry.Name(), "err", err)
			continue
		}
		if !ok || !matchesFilter(summary, filter) {
			continue
		}
		summaries = append(summaries, summary)
	}

	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].StartedAt.Equal(summaries[j].StartedAt) {
			return summaries[i].RunID > summaries[j].RunID
		}
		return summaries[i].StartedAt.After(summaries[j].StartedAt)
	})
	if filter.Limit > 0 && len(summaries) > filter.Limit {
		summaries = summaries[:filter.Limit]
	}
	return summaries, nil
}

func readRun(dir, fallbackID string) (Summary, bool, error) {
	f, err := os.Open(filepath.Join(dir, "metrics.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return Summary{}, false, nil
		}
		return Summary{}, false, fmt.Errorf("runs list: open metrics: %w", err)
	}
	defer f.Close()

	var (
		started      time.Time
		complete     metrics.Event
		found        bool
		count        int
		failedEvents int
	)
	scan := bufio.NewScanner(f)
	for scan.Scan() {
		var event metrics.Event
		if err := json.Unmarshal(scan.Bytes(), &event); err != nil {
			return Summary{}, false, fmt.Errorf("runs list: parse metrics: %w", err)
		}
		count++
		if event.Status != "" && event.Status != "ok" {
			failedEvents++
		}
		if started.IsZero() {
			started = parseTime(event.Timestamp)
		}
		if event.EventType == runCompleteEvent {
			complete = event
			found = true
		}
	}
	if err := scan.Err(); err != nil {
		return Summary{}, false, fmt.Errorf("runs list: read metrics: %w", err)
	}
	if !found {
		return Summary{}, false, nil
	}
	if started.IsZero() {
		started = parseTime(complete.Timestamp)
	}

	return Summary{
		RunID:           runID(fallbackID, complete.Extra),
		ImageRef:        complete.ImageRef,
		VMName:          complete.VMName,
		Status:          complete.Status,
		TotalDurationMS: complete.DurationMS,
		ExitCode:        exitCode(complete.Extra),
		StartedAt:       started,
		EventCount:      count,
		FailedEvents:    failedEvents,
	}, true, nil
}

func matchesFilter(summary Summary, filter Filter) bool {
	switch filter.Status {
	case "", "all":
	case "ok":
		if summary.Status != "ok" {
			return false
		}
	case "fail":
		if summary.Status == "ok" {
			return false
		}
	default:
		return false
	}

	if filter.Since > 0 {
		now := filter.Now
		if now.IsZero() {
			now = time.Now()
		}
		if summary.StartedAt.Before(now.Add(-filter.Since)) {
			return false
		}
	}
	return true
}

func runID(fallback string, extra map[string]any) string {
	if extra == nil {
		return fallback
	}
	if s, ok := extra["run_id"].(string); ok && s != "" {
		return s
	}
	return fallback
}

func exitCode(extra map[string]any) *int {
	if extra == nil {
		return nil
	}
	switch v := extra["exit_code"].(type) {
	case float64:
		i := int(v)
		if float64(i) == v {
			return &i
		}
	case int:
		i := v
		return &i
	case json.Number:
		i64, err := v.Int64()
		if err == nil {
			i := int(i64)
			return &i
		}
	}
	return nil
}

func parseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
