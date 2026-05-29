package guibench

import (
	"bytes"
	"strings"
	"testing"
)

// sampleReport builds a small two-provider report with one flagged cell and a
// human baseline, used across the rendering and round-trip tests.
func sampleReport(t *testing.T) *ScoreReport {
	t.Helper()
	outcomes := []Outcome{
		{Provider: "claude", TaskID: "finder-1", Domain: "Finder", Run: 0, Score: 1, Status: StatusScored},
		{Provider: "claude", TaskID: "finder-1", Domain: "Finder", Run: 1, Score: 1, Status: StatusScored},
		{Provider: "claude", TaskID: "safari-1", Domain: "Safari", Run: 0, Score: 0, Status: StatusScored},
		{Provider: "claude", TaskID: "safari-1", Domain: "Safari", Run: 1, Score: 1, Status: StatusScored}, // flagged
		{Provider: "gpt", TaskID: "finder-1", Domain: "Finder", Run: 0, Score: 0, Status: StatusScored},
		{Provider: "gpt", TaskID: "finder-1", Domain: "Finder", Run: 1, Score: 0, Status: StatusScored},
		{Provider: "gpt", TaskID: "safari-1", Domain: "Safari", Run: 0, Score: 1, Status: StatusScored},
		{Provider: "gpt", TaskID: "safari-1", Domain: "Safari", Run: 1, Score: 1, Status: StatusScored},
	}
	baseline := &HumanBaseline{
		Overall: 0.9,
		Domains: []DomainScore{{Domain: "Finder", Mean: 1.0}, {Domain: "Safari", Mean: 0.8}},
		Source:  "operator pilot",
	}
	rep, err := Aggregate(outcomes, 2, testMeta(), baseline)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	return rep
}

func TestRenderMarkdown(t *testing.T) {
	rep := sampleReport(t)
	var buf bytes.Buffer
	if err := RenderMarkdown(&buf, rep); err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}
	out := buf.String()

	mustContain := []string{
		"# macOS GUI-agent benchmark results",
		"## Provenance",
		"Apple M4 Max", // host hardware
		"0afd5b19",     // cove commit
		"v1:deadbeef",  // corpus version
		"v1:cafebabe",  // verifier hash
		"## Overall success rate",
		"## Per-domain success rate",
		"## Per-task success rate",
		"## Variance-flagged cells",
		"**human**",      // baseline row
		"operator pilot", // baseline source
		"claude",
		"gpt",
		"Finder",
		"Safari",
		"safari-1", // the flagged cell appears in the flagged section
	}
	for _, sub := range mustContain {
		if !strings.Contains(out, sub) {
			t.Errorf("markdown missing %q\n---\n%s", sub, out)
		}
	}

	// The flagged section must name claude/safari-1 (spread 1.0) and not list a
	// stable cell like claude/finder-1.
	flaggedSection := out[strings.Index(out, "## Variance-flagged cells"):]
	if !strings.Contains(flaggedSection, "safari-1") {
		t.Errorf("flagged section missing safari-1:\n%s", flaggedSection)
	}
	if strings.Contains(flaggedSection, "None.") {
		t.Errorf("flagged section says None but a cell is flagged:\n%s", flaggedSection)
	}
}

func TestRenderMarkdownNotRecorded(t *testing.T) {
	// A report with no provenance must render visible placeholders, never blanks
	// that read as measured zeros.
	outcomes := []Outcome{{Provider: "claude", TaskID: "t1", Run: 0, Score: 1, Status: StatusScored}}
	rep, err := Aggregate(outcomes, 1, Meta{}, nil)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	var buf bytes.Buffer
	if err := RenderMarkdown(&buf, rep); err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "(not recorded)") {
		t.Errorf("missing (not recorded) placeholder for empty provenance:\n%s", out)
	}
	// No flagged cells -> the flagged section says None.
	if !strings.Contains(out, "None.") {
		t.Errorf("expected None. in flagged section:\n%s", out)
	}
}

func TestReportJSONRoundTrip(t *testing.T) {
	rep := sampleReport(t)
	var buf bytes.Buffer
	if err := rep.WriteJSON(&buf); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	got, err := ReadReport(&buf)
	if err != nil {
		t.Fatalf("ReadReport: %v", err)
	}
	if got.CorpusVersion != rep.CorpusVersion || got.VerifierHash != rep.VerifierHash {
		t.Errorf("versions not preserved: %+v", got)
	}
	if len(got.Cells) != len(rep.Cells) {
		t.Errorf("cells = %d, want %d", len(got.Cells), len(rep.Cells))
	}
	if got.FlaggedCells != rep.FlaggedCells {
		t.Errorf("flagged cells = %d, want %d", got.FlaggedCells, rep.FlaggedCells)
	}
	if got.HumanBaseline == nil || got.HumanBaseline.Source != "operator pilot" {
		t.Errorf("human baseline not preserved: %+v", got.HumanBaseline)
	}

	// Rendering the round-tripped report must match rendering the original.
	var a, b bytes.Buffer
	if err := RenderMarkdown(&a, rep); err != nil {
		t.Fatal(err)
	}
	if err := RenderMarkdown(&b, got); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Error("markdown differs after JSON round-trip")
	}
}
