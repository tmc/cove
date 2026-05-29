package guibench

import (
	"bufio"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Trace is a parsed run bundle ready to render as a local timeline (design 047
// §16, the local interactive trace viewer). It is built from an on-disk bundle
// directory — the ~/.vz/runs/<id>/ shape RunBundle writes (events.jsonl,
// screenshots/, manifest.json) — so a step-by-step replay needs no cloud
// service, closing trycua's cb-trace-view / app.hud.ai product-clarity lead.
//
// A Trace carries only what the viewer renders; the heavy artifacts
// (screenshots) stay on disk and are referenced by relative path, so the emitted
// HTML embeds no binary assets and opens straight from the bundle directory.
type Trace struct {
	// RunID is the bundle's run id (its directory name, or the manifest's run_id).
	RunID string
	// VMName and ForkFrom come from manifest.json when present.
	VMName   string
	ForkFrom string
	// StartedAt and ExitStatus come from manifest.json when present.
	StartedAt  string
	ExitStatus string
	// Steps is the ordered timeline parsed from events.jsonl.
	Steps []TraceStep
	// Screenshots lists the screenshot file names found in screenshots/, sorted,
	// for steps that reference a capture and for an appendix gallery.
	Screenshots []string
}

// TraceStep is one rendered timeline entry: a control-socket event with the
// fields the viewer surfaces. events.jsonl is free-form (RunBundle.AppendEvent
// writes arbitrary maps), so a step keeps the well-known fields it recognizes
// and stashes the rest in Extra for display, rather than discarding unknown
// telemetry.
type TraceStep struct {
	// Index is the 1-based step number in the timeline.
	Index int
	// Timestamp is the event's "ts" field (RFC3339Nano as RunBundle writes it).
	Timestamp string
	// Event is the event's "event"/"type" label (e.g. "control", "run.start").
	Event string
	// Action, Observation, and Score are surfaced when the event carries them, so
	// the per-step view reads action -> observation -> score (the OSWorld replay
	// shape). They are empty when absent.
	Action      string
	Observation string
	Score       string
	// Error is the event's error string, when present.
	Error string
	// Screenshot is the relative path (under the bundle dir) of a capture this
	// step references, or empty. The viewer links it; it never inlines bytes.
	Screenshot string
	// Extra holds any remaining event fields, sorted by key for stable rendering.
	Extra []TraceField
}

// TraceField is a single extra key/value pair on a step, rendered verbatim.
type TraceField struct {
	Key   string
	Value string
}

// LoadTrace reads the run bundle at dir into a [Trace]: it parses events.jsonl
// into ordered steps, lists screenshots/, and folds in manifest.json metadata
// when present. A missing events.jsonl is not an error — a bundle may have only
// a manifest — but a dir that does not exist is. It touches no VM and no network.
func LoadTrace(dir string) (*Trace, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("load trace: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("load trace: %s is not a directory", dir)
	}
	tr := &Trace{RunID: filepath.Base(dir)}

	if shots, err := listScreenshots(dir); err != nil {
		return nil, err
	} else {
		tr.Screenshots = shots
	}

	if f, err := os.Open(filepath.Join(dir, "events.jsonl")); err == nil {
		defer f.Close()
		steps, err := parseEvents(f)
		if err != nil {
			return nil, err
		}
		tr.Steps = steps
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("load trace: open events: %w", err)
	}

	if err := tr.loadManifest(dir); err != nil {
		return nil, err
	}
	return tr, nil
}

// loadManifest folds run-level metadata from manifest.json into the trace. A
// missing manifest is fine (the bundle may predate finalize); a malformed one
// is an error so a corrupt bundle is not silently rendered as empty.
func (tr *Trace) loadManifest(dir string) error {
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load trace: read manifest: %w", err)
	}
	var m struct {
		RunID      string `json:"run_id"`
		VMName     string `json:"vm_name"`
		ForkFrom   string `json:"fork_from"`
		StartedAt  string `json:"started_at"`
		ExitStatus string `json:"exit_status"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return fmt.Errorf("load trace: parse manifest: %w", err)
	}
	if m.RunID != "" {
		tr.RunID = m.RunID
	}
	tr.VMName = m.VMName
	tr.ForkFrom = m.ForkFrom
	tr.StartedAt = m.StartedAt
	tr.ExitStatus = m.ExitStatus
	return nil
}

// parseEvents reads events.jsonl (one JSON object per line) into ordered steps.
// A blank line is skipped; a malformed line is an error so a corrupt log is not
// silently truncated. Well-known fields are lifted onto the step; the rest land
// in Extra.
func parseEvents(r io.Reader) ([]TraceStep, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	var steps []TraceStep
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			return nil, fmt.Errorf("load trace: events.jsonl line %d: %w", line, err)
		}
		steps = append(steps, stepFromEvent(len(steps)+1, ev))
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("load trace: read events: %w", err)
	}
	return steps, nil
}

// stepFromEvent lifts the well-known fields off an event map onto a [TraceStep],
// stashing the remainder in Extra. It recognizes the field aliases the bundle
// and the agent loop use (event/type, action, observation/result, score,
// screenshot/image, error), so a step renders the same regardless of which
// producer wrote the line.
func stepFromEvent(index int, ev map[string]any) TraceStep {
	step := TraceStep{Index: index}
	consumed := map[string]bool{}
	take := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := ev[k]; ok {
				consumed[k] = true
				return scalar(v)
			}
		}
		return ""
	}
	step.Timestamp = take("ts", "timestamp", "time")
	step.Event = take("event", "type", "req_type", "event_type")
	step.Action = take("action", "command", "input")
	step.Observation = take("observation", "result", "output", "stdout")
	step.Score = take("score")
	step.Error = take("error", "err")
	step.Screenshot = screenshotRef(take("screenshot", "image", "capture"))

	keys := make([]string, 0, len(ev))
	for k := range ev {
		if consumed[k] {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		step.Extra = append(step.Extra, TraceField{Key: k, Value: scalar(ev[k])})
	}
	return step
}

// screenshotRef normalizes a screenshot reference to a path relative to the
// bundle dir. A bare name (e.g. "step1.png") is rooted under screenshots/; a
// value that already names the screenshots/ subdir is left as-is. Empty stays
// empty.
func screenshotRef(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	v = filepath.ToSlash(v)
	if strings.HasPrefix(v, "screenshots/") {
		return v
	}
	return "screenshots/" + filepath.Base(v)
}

// scalar renders an event value as a display string: strings verbatim, numbers
// and bools via their JSON form, and any composite re-marshaled to compact JSON
// so a structured field still renders rather than printing a Go map address.
func scalar(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		// JSON numbers decode to float64; render an integral value without the
		// trailing ".0" and a fractional value with minimal digits.
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%g", x)
	case bool:
		return fmt.Sprintf("%t", x)
	case nil:
		return ""
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
}

// listScreenshots returns the sorted file names in the bundle's screenshots/
// directory. A missing directory yields an empty slice and no error.
func listScreenshots(dir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(dir, "screenshots"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load trace: read screenshots: %w", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

// RenderHTML writes the trace as a self-contained local HTML timeline to w. The
// page references screenshots by relative path (never inlining bytes), so it
// opens straight from the bundle directory with no cloud dependency and no
// embedded binary assets. The output is deterministic for a given trace.
func (tr *Trace) RenderHTML(w io.Writer) error {
	if err := traceTemplate.Execute(w, tr); err != nil {
		return fmt.Errorf("render trace html: %w", err)
	}
	return nil
}

// WriteHTML renders the trace to index.html inside the bundle directory and
// returns the written path, so a caller can print its file:// URL. The file is
// overwritten on each call.
func (tr *Trace) WriteHTML(dir string) (string, error) {
	path := filepath.Join(dir, "index.html")
	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("write trace html: %w", err)
	}
	if err := tr.RenderHTML(f); err != nil {
		f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("write trace html: %w", err)
	}
	return path, nil
}

// HasField reports whether the step has any non-empty surfaced field beyond its
// event label and timestamp, used by the template to decide whether to render
// the detail block.
func (s TraceStep) HasDetail() bool {
	return s.Action != "" || s.Observation != "" || s.Score != "" ||
		s.Error != "" || s.Screenshot != "" || len(s.Extra) > 0
}

// traceTemplate is the self-contained timeline page. All styling is inline so
// the file needs no sibling CSS; screenshots are referenced by relative path so
// no bytes are embedded. The template is parsed once at init.
var traceTemplate = template.Must(template.New("trace").Parse(traceHTML))

const traceHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>cove run {{.RunID}}</title>
<style>
  :root { color-scheme: light dark; }
  body { font: 14px/1.5 -apple-system, system-ui, sans-serif; margin: 0; padding: 2rem; }
  h1 { font-size: 1.4rem; margin: 0 0 0.25rem; }
  .meta { color: #888; margin-bottom: 1.5rem; }
  .meta span { margin-right: 1.25rem; }
  .step { border: 1px solid #8884; border-radius: 8px; padding: 1rem; margin-bottom: 1rem; }
  .step h2 { font-size: 1rem; margin: 0 0 0.5rem; }
  .step .idx { color: #888; font-weight: normal; }
  .step .ts { color: #888; font-weight: normal; font-size: 0.85rem; }
  .field { margin: 0.25rem 0; }
  .field .k { color: #888; display: inline-block; min-width: 7rem; vertical-align: top; }
  .field .v { white-space: pre-wrap; word-break: break-word; }
  .err .v { color: #c0392b; }
  img.shot { max-width: 100%; border: 1px solid #8884; border-radius: 6px; margin-top: 0.5rem; }
  .empty { color: #888; }
  footer { color: #888; margin-top: 2rem; font-size: 0.85rem; }
</style>
</head>
<body>
<h1>cove run {{.RunID}}</h1>
<div class="meta">
  {{if .VMName}}<span>vm: {{.VMName}}</span>{{end}}
  {{if .ForkFrom}}<span>fork-from: {{.ForkFrom}}</span>{{end}}
  {{if .StartedAt}}<span>started: {{.StartedAt}}</span>{{end}}
  {{if .ExitStatus}}<span>exit: {{.ExitStatus}}</span>{{end}}
  <span>steps: {{len .Steps}}</span>
  <span>screenshots: {{len .Screenshots}}</span>
</div>
{{if .Steps}}
{{range .Steps}}
<div class="step">
  <h2><span class="idx">#{{.Index}}</span> {{if .Event}}{{.Event}}{{else}}(event){{end}} {{if .Timestamp}}<span class="ts">{{.Timestamp}}</span>{{end}}</h2>
  {{if .HasDetail}}
    {{if .Action}}<div class="field"><span class="k">action</span><span class="v">{{.Action}}</span></div>{{end}}
    {{if .Observation}}<div class="field"><span class="k">observation</span><span class="v">{{.Observation}}</span></div>{{end}}
    {{if .Score}}<div class="field"><span class="k">score</span><span class="v">{{.Score}}</span></div>{{end}}
    {{if .Error}}<div class="field err"><span class="k">error</span><span class="v">{{.Error}}</span></div>{{end}}
    {{range .Extra}}<div class="field"><span class="k">{{.Key}}</span><span class="v">{{.Value}}</span></div>{{end}}
    {{if .Screenshot}}<div><img class="shot" src="{{.Screenshot}}" alt="screenshot for step {{.Index}}"></div>{{end}}
  {{else}}<div class="empty">no detail</div>{{end}}
</div>
{{end}}
{{else}}
<p class="empty">No timeline events recorded in this run bundle.</p>
{{end}}
{{if .Screenshots}}
<h2>Screenshots</h2>
{{range .Screenshots}}
<div><img class="shot" src="screenshots/{{.}}" alt="{{.}}"></div>
{{end}}
{{end}}
<footer>Generated locally by cove bench gui view — no cloud dependency. Screenshots referenced by relative path.</footer>
</body>
</html>
`
