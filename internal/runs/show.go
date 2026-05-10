package runs

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tmc/vz-macos/internal/metrics"
)

var lifecycleEvents = map[string]bool{
	"fork_created":              true,
	"vm_create":                 true,
	"vm_start":                  true,
	"agent_ready":               true,
	"build_step":                true,
	"benchmark_result":          true,
	"lifecycle.budget.exceeded": true,
	"lifecycle.idle.tripped":    true,
	"lifecycle.maxage.tripped":  true,
	runCompleteEvent:            true,
}

// Show is the rendered data for a run.
type Show struct {
	RunID         string
	Dir           string
	Events        []metrics.Event
	Lifecycle     []metrics.Event
	Result        Result
	Artifacts     []string
	ArtifactBytes int64
	Failure       Failure
}

// Result summarizes the terminal run status.
type Result struct {
	Status       string
	ExitCode     int
	HasExitCode  bool
	WallclockMS  int64
	FailedEvents int
}

// Failure summarizes the first failed event in a run.
type Failure struct {
	Class  string
	Reason string
}

// LoadShow loads show data for the run matching prefix under root.
func LoadShow(root, prefix string) (Show, error) {
	dir, err := matchRunDir(root, prefix)
	if err != nil {
		return Show{}, err
	}
	events, err := readEvents(filepath.Join(dir, "metrics.jsonl"))
	if err != nil {
		return Show{}, err
	}
	artifacts, artifactBytes, err := listArtifacts(dir)
	if err != nil {
		return Show{}, err
	}
	show := Show{
		RunID:         filepath.Base(dir),
		Dir:           dir,
		Events:        events,
		Lifecycle:     lifecycle(events),
		Result:        result(events),
		Artifacts:     artifacts,
		ArtifactBytes: artifactBytes,
	}
	if show.Result.Status != "" && show.Result.Status != "ok" {
		show.Failure = failure(events)
	}
	return show, nil
}

// RenderShow writes a plain text summary for show.
func RenderShow(w io.Writer, show Show) error {
	if w == nil {
		return fmt.Errorf("render show: nil writer")
	}
	if _, err := fmt.Fprintf(w, "Run: %s\n", show.RunID); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Directory: %s\n\n", show.Dir); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "Lifecycle:"); err != nil {
		return err
	}
	for _, e := range show.Lifecycle {
		if _, err := fmt.Fprintf(w, "  %s  %s  %dms\n", e.EventType, eventStatus(e), e.DurationMS); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Result: %s", show.Result.Status); err != nil {
		return err
	}
	if show.Result.HasExitCode {
		if _, err := fmt.Fprintf(w, " exit_code=%d", show.Result.ExitCode); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, " wallclock=%dms", show.Result.WallclockMS); err != nil {
		return err
	}
	if show.Result.FailedEvents > 0 {
		if _, err := fmt.Fprintf(w, " failed_events=%d", show.Result.FailedEvents); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if show.Failure.Class != "" {
		if _, err := fmt.Fprintf(w, "Failure: %s: %s\n", show.Failure.Class, show.Failure.Reason); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "\nArtifacts (%d bytes):\n", show.ArtifactBytes); err != nil {
		return err
	}
	for _, name := range show.Artifacts {
		if _, err := fmt.Fprintf(w, "  %s\n", name); err != nil {
			return err
		}
	}
	return nil
}

func matchRunDir(root, prefix string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("runs root is empty")
	}
	if prefix == "" {
		return "", fmt.Errorf("run prefix is empty")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", fmt.Errorf("read runs root: %w", err)
	}
	var matches []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), prefix) {
			matches = append(matches, e.Name())
		}
	}
	sort.Strings(matches)
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("run %q: not found", prefix)
	case 1:
		return filepath.Join(root, matches[0]), nil
	default:
		return "", fmt.Errorf("run %q: ambiguous: %s", prefix, strings.Join(matches, ", "))
	}
}

func readEvents(path string) ([]metrics.Event, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open metrics: %w", err)
	}
	defer f.Close()

	var events []metrics.Event
	scan := bufio.NewScanner(f)
	for line := 1; scan.Scan(); line++ {
		var e metrics.Event
		if err := json.Unmarshal(scan.Bytes(), &e); err != nil {
			return nil, fmt.Errorf("read metrics line %d: %w", line, err)
		}
		events = append(events, e)
	}
	if err := scan.Err(); err != nil {
		return nil, fmt.Errorf("read metrics: %w", err)
	}
	return events, nil
}

func lifecycle(events []metrics.Event) []metrics.Event {
	var out []metrics.Event
	for _, e := range events {
		if lifecycleEvents[e.EventType] {
			out = append(out, e)
		}
	}
	return out
}

func result(events []metrics.Event) Result {
	var r Result
	for _, e := range events {
		if e.Status != "" && e.Status != "ok" {
			r.FailedEvents++
		}
		if e.EventType != runCompleteEvent {
			continue
		}
		r.Status = e.Status
		r.WallclockMS = e.DurationMS
		if code := exitCode(e.Extra); code != nil {
			r.ExitCode = *code
			r.HasExitCode = true
		}
	}
	return r
}

func failure(events []metrics.Event) Failure {
	for _, e := range events {
		if e.Status == "" || e.Status == "ok" {
			continue
		}
		reason := extraString(e.Extra, "reason")
		if reason == "" {
			reason = extraString(e.Extra, "error")
		}
		if reason == "" {
			reason = e.Status
		}
		return Failure{Class: e.EventType, Reason: shortReason(reason)}
	}
	return Failure{}
}

func listArtifacts(dir string) ([]string, int64, error) {
	var names []string
	var total int64
	if err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		names = append(names, filepath.ToSlash(name))
		if info, ierr := d.Info(); ierr == nil {
			total += info.Size()
		}
		return nil
	}); err != nil {
		return nil, 0, fmt.Errorf("list artifacts: %w", err)
	}
	sort.Strings(names)
	return names, total, nil
}

func eventStatus(e metrics.Event) string {
	if e.Status != "" {
		return e.Status
	}
	return "-"
}

func extraString(extra map[string]any, key string) string {
	if extra == nil {
		return ""
	}
	v, ok := extra[key]
	if !ok {
		return ""
	}
	switch v := v.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprint(v)
	}
}

func shortReason(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		return s[:117] + "..."
	}
	return s
}
