package guibench

import (
	"math"
	"strings"
	"testing"
)

// meta returns a fully populated Meta for tests so provenance assertions have
// something to check.
func testMeta() Meta {
	return Meta{
		GeneratedAt:   "2026-05-29T00:00:00Z",
		CoveCommit:    "0afd5b19",
		HostHardware:  "Apple M4 Max",
		CorpusVersion: "v1:deadbeef",
		VerifierHash:  "v1:cafebabe",
		Model:         "test-model",
	}
}

func TestAggregateCellStatistics(t *testing.T) {
	tests := []struct {
		name        string
		scores      []float64
		wantMean    float64
		wantSpread  float64
		wantFlagged bool
	}{
		{"all pass", []float64{1, 1, 1}, 1, 0, false},
		{"all fail", []float64{0, 0, 0}, 0, 0, false},
		{"two of three", []float64{1, 1, 0}, 2.0 / 3.0, 1, true},
		{"at threshold not flagged", []float64{0.6, 0.4, 0.5}, 0.5, 0.2, false},
		{"just over threshold flagged", []float64{0.7, 0.4, 0.5}, 0.5333333333, 0.3, true},
		{"single run never flagged", []float64{1}, 1, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outcomes := make([]Outcome, len(tt.scores))
			for i, s := range tt.scores {
				outcomes[i] = Outcome{Provider: "claude", TaskID: "t1", Run: i, Score: s, Status: StatusScored}
			}
			rep, err := Aggregate(outcomes, len(tt.scores), testMeta(), nil)
			if err != nil {
				t.Fatalf("Aggregate: %v", err)
			}
			if len(rep.Cells) != 1 {
				t.Fatalf("got %d cells, want 1", len(rep.Cells))
			}
			c := rep.Cells[0]
			if math.Abs(c.Mean-tt.wantMean) > 1e-9 {
				t.Errorf("mean = %v, want %v", c.Mean, tt.wantMean)
			}
			if math.Abs(c.Spread-tt.wantSpread) > 1e-9 {
				t.Errorf("spread = %v, want %v", c.Spread, tt.wantSpread)
			}
			if c.Flagged != tt.wantFlagged {
				t.Errorf("flagged = %v, want %v", c.Flagged, tt.wantFlagged)
			}
		})
	}
}

func TestAggregateMatrix(t *testing.T) {
	// Two providers, two domains, two tasks each, two runs per cell.
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
	rep, err := Aggregate(outcomes, 2, testMeta(), nil)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}

	if got := rep.TaskIDs; len(got) != 2 || got[0] != "finder-1" || got[1] != "safari-1" {
		t.Errorf("task ids = %v, want [finder-1 safari-1]", got)
	}
	if got := rep.Domains; len(got) != 2 || got[0] != "Finder" || got[1] != "Safari" {
		t.Errorf("domains = %v, want [Finder Safari]", got)
	}
	if rep.Runs != 2 {
		t.Errorf("runs = %d, want 2", rep.Runs)
	}
	if rep.FlaggedCells != 1 {
		t.Errorf("flagged cells = %d, want 1 (claude/safari-1 spread 1.0)", rep.FlaggedCells)
	}

	// Providers are sorted: claude before gpt.
	if len(rep.Providers) != 2 {
		t.Fatalf("got %d providers, want 2", len(rep.Providers))
	}
	claude, gpt := rep.Providers[0], rep.Providers[1]
	if claude.Provider != "claude" || gpt.Provider != "gpt" {
		t.Fatalf("provider order = [%s %s], want [claude gpt]", claude.Provider, gpt.Provider)
	}
	// claude overall = mean(finder mean 1.0, safari mean 0.5) = 0.75.
	if math.Abs(claude.Overall-0.75) > 1e-9 {
		t.Errorf("claude overall = %v, want 0.75", claude.Overall)
	}
	// gpt overall = mean(finder 0.0, safari 1.0) = 0.5.
	if math.Abs(gpt.Overall-0.5) > 1e-9 {
		t.Errorf("gpt overall = %v, want 0.5", gpt.Overall)
	}
	if claude.FlaggedCells != 1 {
		t.Errorf("claude flagged cells = %d, want 1", claude.FlaggedCells)
	}
	if gpt.FlaggedCells != 0 {
		t.Errorf("gpt flagged cells = %d, want 0", gpt.FlaggedCells)
	}

	// Per-domain for claude.
	if len(claude.Domains) != 2 {
		t.Fatalf("claude domains = %d, want 2", len(claude.Domains))
	}
	if claude.Domains[0].Domain != "Finder" || math.Abs(claude.Domains[0].Mean-1) > 1e-9 {
		t.Errorf("claude Finder = %+v, want mean 1", claude.Domains[0])
	}
	if claude.Domains[1].Domain != "Safari" || math.Abs(claude.Domains[1].Mean-0.5) > 1e-9 {
		t.Errorf("claude Safari = %+v, want mean 0.5", claude.Domains[1])
	}
}

func TestAggregateMetadataAndModel(t *testing.T) {
	outcomes := []Outcome{
		{Provider: "claude", TaskID: "t1", Run: 0, Score: 1, Status: StatusScored},
	}
	rep, err := Aggregate(outcomes, 1, testMeta(), nil)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if rep.CoveCommit != "0afd5b19" || rep.HostHardware != "Apple M4 Max" {
		t.Errorf("provenance not propagated: %+v", rep)
	}
	if rep.CorpusVersion != "v1:deadbeef" || rep.VerifierHash != "v1:cafebabe" {
		t.Errorf("versions not propagated: corpus=%q verifier=%q", rep.CorpusVersion, rep.VerifierHash)
	}
	if rep.SchemaVersion != SchemaVersion {
		t.Errorf("schema version = %q, want %q", rep.SchemaVersion, SchemaVersion)
	}
	if rep.Providers[0].Model != "test-model" {
		t.Errorf("model = %q, want test-model", rep.Providers[0].Model)
	}
}

func TestAggregateErrorOutcomeCountsAsZero(t *testing.T) {
	// A crashed run is a zero, not a dropped row (AndroidWorld discipline).
	outcomes := []Outcome{
		{Provider: "claude", TaskID: "t1", Run: 0, Score: 1, Status: StatusScored},
		{Provider: "claude", TaskID: "t1", Run: 1, Score: 0, Status: StatusError, Error: "fork timed out"},
	}
	rep, err := Aggregate(outcomes, 2, testMeta(), nil)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	c := rep.Cells[0]
	if c.Errors != 1 {
		t.Errorf("errors = %d, want 1", c.Errors)
	}
	if math.Abs(c.Mean-0.5) > 1e-9 {
		t.Errorf("mean = %v, want 0.5 (crash counts as 0)", c.Mean)
	}
	if c.Runs != 2 {
		t.Errorf("runs = %d, want 2", c.Runs)
	}
}

func TestAggregateEmptyDomainSentinel(t *testing.T) {
	outcomes := []Outcome{
		{Provider: "claude", TaskID: "t1", Run: 0, Score: 1, Status: StatusScored},
	}
	rep, err := Aggregate(outcomes, 1, testMeta(), nil)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if len(rep.Domains) != 1 || rep.Domains[0] != "(none)" {
		t.Errorf("domains = %v, want [(none)]", rep.Domains)
	}
	if rep.Providers[0].Domains[0].Domain != "(none)" {
		t.Errorf("provider domain = %q, want (none)", rep.Providers[0].Domains[0].Domain)
	}
}

func TestAggregateDeterministicOrder(t *testing.T) {
	// Outcomes in arrival-scrambled order must yield the same cell order.
	scrambled := []Outcome{
		{Provider: "gpt", TaskID: "z", Run: 1, Score: 1, Status: StatusScored},
		{Provider: "claude", TaskID: "a", Run: 0, Score: 1, Status: StatusScored},
		{Provider: "gpt", TaskID: "a", Run: 0, Score: 1, Status: StatusScored},
		{Provider: "claude", TaskID: "z", Run: 0, Score: 1, Status: StatusScored},
		{Provider: "gpt", TaskID: "z", Run: 0, Score: 1, Status: StatusScored},
		{Provider: "claude", TaskID: "a", Run: 1, Score: 1, Status: StatusScored},
	}
	rep, err := Aggregate(scrambled, 2, testMeta(), nil)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	var got []string
	for _, c := range rep.Cells {
		got = append(got, c.Provider+"/"+c.TaskID)
	}
	want := []string{"claude/a", "claude/z", "gpt/a", "gpt/z"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("cell order = %v, want %v", got, want)
	}
	// Scores within a cell are ordered by run index.
	if c := rep.Cells[0]; len(c.Scores) != 2 {
		t.Errorf("claude/a scores = %v, want 2 entries", c.Scores)
	}
}

func TestAggregateRejectsBadInput(t *testing.T) {
	tests := []struct {
		name     string
		outcomes []Outcome
		runs     int
	}{
		{"zero runs", []Outcome{{Provider: "c", TaskID: "t", Score: 1}}, 0},
		{"empty provider", []Outcome{{TaskID: "t", Score: 1}}, 1},
		{"empty task", []Outcome{{Provider: "c", Score: 1}}, 1},
		{"score above one", []Outcome{{Provider: "c", TaskID: "t", Score: 1.5}}, 1},
		{"score below zero", []Outcome{{Provider: "c", TaskID: "t", Score: -0.1}}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Aggregate(tt.outcomes, tt.runs, testMeta(), nil); err == nil {
				t.Fatalf("want error for %s", tt.name)
			}
		})
	}
}

func TestAggregateHumanBaseline(t *testing.T) {
	outcomes := []Outcome{
		{Provider: "claude", TaskID: "t1", Domain: "Finder", Run: 0, Score: 1, Status: StatusScored},
	}
	baseline := &HumanBaseline{
		Overall: 0.724,
		Domains: []DomainScore{{Domain: "Finder", Mean: 0.8, Tasks: 1}},
		Source:  "operator pilot 2026-05",
	}
	rep, err := Aggregate(outcomes, 1, testMeta(), baseline)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if rep.HumanBaseline == nil {
		t.Fatal("human baseline dropped")
	}
	if math.Abs(rep.HumanBaseline.Overall-0.724) > 1e-9 {
		t.Errorf("human overall = %v, want 0.724", rep.HumanBaseline.Overall)
	}
}
