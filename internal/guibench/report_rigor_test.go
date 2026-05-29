package guibench

import (
	"bytes"
	"strings"
	"testing"
)

// rigorReport builds a two-task report whose outcomes carry rigor: one Tier-C
// allowlisted task and one Tier-B sqlite task, so the rendered report exercises
// every rigor column.
func rigorReport(t *testing.T) *ScoreReport {
	t.Helper()
	axRigor := RigorOf(taskWith("notes-ax", GetterSpec{Kind: "accessibility", App: "Notes", Attr: "value"}, nil, "icloud.com"))
	sqlRigor := RigorOf(taskWith("safari-sql", GetterSpec{Kind: "sqlite", Path: "/h.db", Query: "SELECT 1"}, nil))
	outcomes := []Outcome{
		{Provider: "claude", TaskID: "notes-ax", Domain: "Notes", Run: 0, Score: 1, Status: StatusScored, Rigor: &axRigor},
		{Provider: "claude", TaskID: "safari-sql", Domain: "Safari", Run: 0, Score: 0, Status: StatusScored, Rigor: &sqlRigor},
	}
	rep, err := Aggregate(outcomes, 1, testMeta(), nil)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	return rep
}

func TestRenderMarkdownRigor(t *testing.T) {
	rep := rigorReport(t)
	var buf bytes.Buffer
	if err := RenderMarkdown(&buf, rep); err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}
	out := buf.String()

	mustContain := []string{
		"## Verification rigor",
		"## Per-task verification rigor",
		"fork-per-task",                     // isolation column
		"deny-all",                          // sqlite task egress
		"allow: icloud.com",                 // allowlisted task egress
		"C (Apple Events + Accessibility)",  // Tier-C grant
		"B (Full Disk Access)",              // Tier-B grant
		"cfprefsd, wal",                     // sqlite task flushes
		"Checkpoint SQLite WAL before read", // summary row
	}
	for _, sub := range mustContain {
		if !strings.Contains(out, sub) {
			t.Errorf("rigor markdown missing %q\n---\n%s", sub, out)
		}
	}

	// The headline claim must appear in the rigor section.
	rigorSection := out[strings.Index(out, "## Verification rigor"):]
	if !strings.Contains(rigorSection, "2 scored") {
		t.Errorf("rigor section missing task count headline:\n%s", rigorSection)
	}
}

// TestRenderMarkdownNoRigorOmitsSection guards that a rigorless report (the
// existing fixture shape) does not print empty rigor sections.
func TestRenderMarkdownNoRigorOmitsSection(t *testing.T) {
	rep := sampleReport(t) // sampleReport's outcomes carry no rigor
	var buf bytes.Buffer
	if err := RenderMarkdown(&buf, rep); err != nil {
		t.Fatalf("RenderMarkdown: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "## Verification rigor") {
		t.Errorf("rigorless report rendered a Verification rigor section:\n%s", out)
	}
	if strings.Contains(out, "## Per-task verification rigor") {
		t.Errorf("rigorless report rendered a Per-task verification rigor section:\n%s", out)
	}
}

// TestReportRigorJSONRoundTrip confirms rigor survives the score.json round trip
// and renders identically.
func TestReportRigorJSONRoundTrip(t *testing.T) {
	rep := rigorReport(t)
	var buf bytes.Buffer
	if err := rep.WriteJSON(&buf); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	got, err := ReadReport(&buf)
	if err != nil {
		t.Fatalf("ReadReport: %v", err)
	}
	if got.Rigor == nil || got.Rigor.Tasks != rep.Rigor.Tasks {
		t.Fatalf("rigor summary not preserved: %+v", got.Rigor)
	}
	var a, b bytes.Buffer
	if err := RenderMarkdown(&a, rep); err != nil {
		t.Fatal(err)
	}
	if err := RenderMarkdown(&b, got); err != nil {
		t.Fatal(err)
	}
	if a.String() != b.String() {
		t.Error("rigor markdown differs after JSON round-trip")
	}
}
