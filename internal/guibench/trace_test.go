package guibench

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// writeTraceBundle creates a synthetic run bundle dir under t.TempDir(): a
// manifest.json, an events.jsonl with assorted event shapes, and a screenshots/
// directory with one tiny synthetic capture. It returns the bundle dir. It is
// named distinctly from the trajectory tests' writeBundle helper, which builds a
// different bundle shape.
func writeTraceBundle(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "deadbeef")
	if err := os.MkdirAll(filepath.Join(dir, "screenshots"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"run_id":"deadbeef","vm_name":"bench-fork","fork_from":"macos-base","started_at":"2026-05-29T00:00:00Z","exit_status":"ok"}`
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	// events.jsonl: a run.start, an agent step with action+observation+score and a
	// screenshot, a control event with an error, and a blank line (skipped).
	events := strings.Join([]string{
		`{"ts":"2026-05-29T00:00:00.1Z","event":"run.start","run_id":"deadbeef"}`,
		`{"ts":"2026-05-29T00:00:01Z","event":"agent.step","action":"click Finder","observation":"folder created","score":1,"screenshot":"step1.png"}`,
		``,
		`{"ts":"2026-05-29T00:00:02Z","event":"control","req_type":"agent-exec","error":"exit 1","budget":5}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(events), 0o644); err != nil {
		t.Fatal(err)
	}
	// A tiny synthetic PNG header is enough — the viewer references it by path and
	// never decodes it, so a few bytes keep the fixture binary-free in git.
	if err := os.WriteFile(filepath.Join(dir, "screenshots", "step1.png"), []byte("\x89PNG\r\n\x1a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadTrace(t *testing.T) {
	dir := writeTraceBundle(t)
	tr, err := LoadTrace(dir)
	if err != nil {
		t.Fatalf("LoadTrace: %v", err)
	}
	if tr.RunID != "deadbeef" {
		t.Errorf("run id = %q, want deadbeef", tr.RunID)
	}
	if tr.VMName != "bench-fork" || tr.ForkFrom != "macos-base" || tr.ExitStatus != "ok" {
		t.Errorf("manifest metadata not loaded: %+v", tr)
	}
	if len(tr.Steps) != 3 {
		t.Fatalf("steps = %d, want 3 (blank line skipped)", len(tr.Steps))
	}
	if len(tr.Screenshots) != 1 || tr.Screenshots[0] != "step1.png" {
		t.Errorf("screenshots = %v, want [step1.png]", tr.Screenshots)
	}

	step := tr.Steps[1]
	if step.Event != "agent.step" {
		t.Errorf("step 2 event = %q, want agent.step", step.Event)
	}
	if step.Action != "click Finder" || step.Observation != "folder created" {
		t.Errorf("step 2 action/observation = %q/%q", step.Action, step.Observation)
	}
	if step.Score != "1" {
		t.Errorf("step 2 score = %q, want 1 (integral float rendered without .0)", step.Score)
	}
	if step.Screenshot != "screenshots/step1.png" {
		t.Errorf("step 2 screenshot = %q, want screenshots/step1.png", step.Screenshot)
	}

	// The control event keeps its unrecognized "budget" field in Extra.
	ctrl := tr.Steps[2]
	if ctrl.Error != "exit 1" {
		t.Errorf("control step error = %q, want exit 1", ctrl.Error)
	}
	foundBudget := false
	for _, f := range ctrl.Extra {
		if f.Key == "budget" && f.Value == "5" {
			foundBudget = true
		}
	}
	if !foundBudget {
		t.Errorf("control step Extra missing budget=5: %+v", ctrl.Extra)
	}
}

func TestRenderHTMLValidAndComplete(t *testing.T) {
	dir := writeTraceBundle(t)
	tr, err := LoadTrace(dir)
	if err != nil {
		t.Fatalf("LoadTrace: %v", err)
	}
	var buf bytes.Buffer
	if err := tr.RenderHTML(&buf); err != nil {
		t.Fatalf("RenderHTML: %v", err)
	}
	out := buf.String()

	// Parse-check: the output must be well-formed HTML.
	if _, err := html.Parse(strings.NewReader(out)); err != nil {
		t.Fatalf("rendered HTML does not parse: %v", err)
	}

	// The steps and their fields must be present in the rendered page.
	for _, want := range []string{
		"cove run deadbeef",
		"bench-fork",
		"agent.step",
		"click Finder",
		"folder created",
		"exit 1",
		`src="screenshots/step1.png"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered HTML missing %q", want)
		}
	}

	// Walk the parsed tree to confirm the screenshot reference is an <img src>
	// pointing at the relative path (no inlined data: URI, no embedded bytes).
	doc, _ := html.Parse(strings.NewReader(out))
	if !hasRelativeImg(doc) {
		t.Errorf("no <img> with a relative screenshots/ src found")
	}
	if strings.Contains(out, "data:image") {
		t.Errorf("HTML embeds a data: image URI; screenshots must be referenced by path")
	}
}

// TestRenderHTMLEscapesContent confirms html/template escapes a hostile field so
// an event value cannot inject markup into the viewer.
func TestRenderHTMLEscapesContent(t *testing.T) {
	tr := &Trace{
		RunID: "x",
		Steps: []TraceStep{{Index: 1, Event: "agent.step", Action: `<script>alert(1)</script>`}},
	}
	var buf bytes.Buffer
	if err := tr.RenderHTML(&buf); err != nil {
		t.Fatalf("RenderHTML: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "<script>alert(1)</script>") {
		t.Errorf("hostile action not escaped:\n%s", out)
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Errorf("expected escaped script tag in output:\n%s", out)
	}
}

func TestWriteHTML(t *testing.T) {
	dir := writeTraceBundle(t)
	tr, err := LoadTrace(dir)
	if err != nil {
		t.Fatalf("LoadTrace: %v", err)
	}
	path, err := tr.WriteHTML(dir)
	if err != nil {
		t.Fatalf("WriteHTML: %v", err)
	}
	if path != filepath.Join(dir, "index.html") {
		t.Errorf("written path = %q, want index.html in bundle", path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	if _, err := html.Parse(bytes.NewReader(b)); err != nil {
		t.Errorf("written index.html does not parse: %v", err)
	}
}

func TestLoadTraceMissingDir(t *testing.T) {
	if _, err := LoadTrace(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("LoadTrace on missing dir returned nil error")
	}
}

// TestLoadTraceEmptyBundle confirms a bundle with no events.jsonl still loads
// (a manifest-only bundle) and renders an empty-timeline page.
func TestLoadTraceEmptyBundle(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "empty")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	tr, err := LoadTrace(dir)
	if err != nil {
		t.Fatalf("LoadTrace empty: %v", err)
	}
	if len(tr.Steps) != 0 {
		t.Errorf("steps = %d, want 0", len(tr.Steps))
	}
	var buf bytes.Buffer
	if err := tr.RenderHTML(&buf); err != nil {
		t.Fatalf("RenderHTML empty: %v", err)
	}
	if !strings.Contains(buf.String(), "No timeline events") {
		t.Errorf("empty bundle page missing empty-state message")
	}
}

// hasRelativeImg walks the parsed HTML for an <img> whose src starts with
// "screenshots/", confirming screenshots are referenced by relative path.
func hasRelativeImg(n *html.Node) bool {
	if n.Type == html.ElementNode && n.Data == "img" {
		for _, a := range n.Attr {
			if a.Key == "src" && strings.HasPrefix(a.Val, "screenshots/") {
				return true
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if hasRelativeImg(c) {
			return true
		}
	}
	return false
}
