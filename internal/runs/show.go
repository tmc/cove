package runs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/tmc/cove/internal/metrics"
)

// ErrRunNotFound reports that no run directory matched the requested prefix.
var ErrRunNotFound = errors.New("run not found")

var lifecycleEvents = map[string]bool{
	forkCreatedEvent:            true,
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
	RunID         string           `json:"run_id"`
	Dir           string           `json:"dir"`
	Events        []metrics.Event  `json:"events"`
	Lifecycle     []metrics.Event  `json:"lifecycle"`
	Result        Result           `json:"result"`
	Fork          *ForkSummary     `json:"fork,omitempty"`
	Network       *NetworkSummary  `json:"network,omitempty"`
	Resource      *ResourceSummary `json:"resource,omitempty"`
	Artifacts     []string         `json:"artifacts"`
	ArtifactBytes int64            `json:"artifact_bytes"`
	TaskRecord    *Record          `json:"task_record,omitempty"`
	Failure       Failure          `json:"failure,omitempty"`
}

// Result summarizes the terminal run status.
type Result struct {
	Status       string `json:"status,omitempty"`
	ExitCode     int    `json:"exit_code,omitempty"`
	HasExitCode  bool   `json:"has_exit_code,omitempty"`
	WallclockMS  int64  `json:"wallclock_ms,omitempty"`
	FailedEvents int    `json:"failed_events,omitempty"`
}

// Failure summarizes the first failed event in a run.
type Failure struct {
	Class  string `json:"class,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// LoadShow loads show data for the run matching prefix under root.
func LoadShow(root, prefix string) (Show, error) {
	dir, err := ResolveDir(root, prefix)
	if err != nil {
		return Show{}, err
	}
	events, metricErr := readEvents(filepath.Join(dir, "metrics.jsonl"))
	var record *Record
	_, manifestErr := os.Lstat(filepath.Join(dir, "manifest.json"))
	_, eventErr := os.Lstat(filepath.Join(dir, "events.jsonl"))
	if manifestErr == nil || eventErr == nil {
		loaded, err := LoadRecord(dir)
		if err != nil {
			return Show{}, err
		}
		record = &loaded
	}
	if metricErr != nil && (!errors.Is(metricErr, os.ErrNotExist) || record == nil) {
		return Show{}, metricErr
	}
	artifacts, artifactBytes, err := listArtifacts(dir)
	if err != nil {
		return Show{}, err
	}
	show := Show{
		RunID:         filepath.Base(dir),
		TaskRecord:    record,
		Dir:           dir,
		Events:        events,
		Lifecycle:     lifecycle(events),
		Result:        result(events),
		Fork:          summarizeFork(events),
		Network:       summarizeNetwork(events),
		Resource:      summarizeResources(events),
		Artifacts:     artifacts,
		ArtifactBytes: artifactBytes,
	}
	if record != nil && (record.Manifest.SchemaVersion != 0 || metricErr != nil) {
		show.Result.Status = record.Manifest.Outcome
		if record.Manifest.PrimaryError != "" {
			show.Failure = Failure{Class: record.Manifest.Outcome, Reason: terminalText(record.Manifest.PrimaryError)}
		}
	}
	if show.Result.Status != "" && show.Result.Status != "ok" {
		if show.Failure.Class == "" {
			show.Failure = failure(events)
		}
	}
	return show, nil
}

// ResolveDir returns the single run directory matching prefix under root.
func ResolveDir(root, prefix string) (string, error) {
	return matchRunDir(root, prefix)
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
	if show.Fork != nil {
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
		if err := renderForkSummary(w, show.Fork); err != nil {
			return err
		}
	}
	if show.Network != nil {
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
		if err := renderNetworkSummary(w, show.Network); err != nil {
			return err
		}
	}
	if show.Resource != nil {
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
		if err := renderResourceSummary(w, show.Resource); err != nil {
			return err
		}
	}
	if show.TaskRecord != nil {
		if err := renderTaskRecord(w, *show.TaskRecord); err != nil {
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
		if os.IsNotExist(err) {
			return "", fmt.Errorf("run %q: %w", prefix, ErrRunNotFound)
		}
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
		return "", fmt.Errorf("run %q: %w", prefix, ErrRunNotFound)
	case 1:
		return filepath.Join(root, matches[0]), nil
	default:
		return "", fmt.Errorf("run %q: ambiguous: %s", prefix, strings.Join(matches, ", "))
	}
}

func readEvents(path string) ([]metrics.Event, error) {
	var events []metrics.Event
	_, err := readJSONLines(path, func(data []byte) error {
		var event metrics.Event
		if err := json.Unmarshal(data, &event); err != nil {
			return err
		}
		events = append(events, event)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("open metrics: %w", err)
	}
	return events, nil
}

func renderTaskRecord(w io.Writer, record Record) error {
	manifest := record.Manifest
	if _, err := fmt.Fprintf(w, "\nTask run: %s attempt=%s outcome=%s incomplete=%t\n", terminalText(manifest.RunID), terminalText(manifest.AttemptID), terminalText(manifest.Outcome), record.Incomplete); err != nil {
		return err
	}
	if manifest.Task != nil {
		task := manifest.Task
		if _, err := fmt.Fprintf(w, "  kind=%s route=%s backend=%s retention=%s\n", terminalText(task.Kind), terminalText(task.GuestRoute), terminalText(task.Backend), terminalText(task.Retention)); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "  image=%s plan=%s source=%s\n", task.ImageDigest, task.PlanDigest, task.SourceDigest); err != nil {
			return err
		}
	}
	for _, event := range record.Events {
		if _, err := fmt.Fprintf(w, "  #%d %s step=%s status=%s %dms\n", event.Sequence, terminalText(event.Kind), terminalText(event.StepID), terminalText(event.Status), event.DurationMS); err != nil {
			return err
		}
	}
	for _, artifact := range manifest.Artifacts {
		if _, err := fmt.Fprintf(w, "  evidence %s: %s %s\n", terminalText(artifact.Name), terminalText(artifact.Status), terminalText(artifact.Reason)); err != nil {
			return err
		}
	}
	for _, message := range manifest.CaptureErrors {
		if _, err := fmt.Fprintf(w, "  capture error: %s\n", terminalText(message)); err != nil {
			return err
		}
	}
	for _, message := range manifest.CleanupErrors {
		if _, err := fmt.Fprintf(w, "  cleanup error: %s\n", terminalText(message)); err != nil {
			return err
		}
	}
	if record.TruncatedEvents {
		_, err := fmt.Fprintln(w, "  final event line was interrupted")
		return err
	}
	return nil
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

func terminalText(value string) string {
	quoted := strconv.Quote(value)
	return quoted[1 : len(quoted)-1]
}
