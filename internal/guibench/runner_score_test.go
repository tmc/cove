package guibench

import (
	"bytes"
	"context"
	"testing"
)

// TestRunToScoreReport exercises the full no-VM slice-2 path: drive a small
// corpus through the runner against a fake backend, aggregate the outcomes into
// a score.json, and assert the emitted report's shape (per-task cells, per-
// domain rollups, overall success rate) without ever touching a VM.
func TestRunToScoreReport(t *testing.T) {
	finder := &Task{
		ID:     "finder-folder",
		Domain: "Finder",
		Image:  "img",
		Evaluator: Evaluator{
			Func:    StringList{"exact_match"},
			Result:  GetterSpec{Kind: "exec", Args: []string{"echo", "ok"}},
			Options: map[string]any{"expected": "ok"},
		},
	}
	settings := &Task{
		ID:     "settings-theme",
		Domain: "Settings",
		Image:  "img",
		Evaluator: Evaluator{
			Func:    StringList{"exact_match"},
			Result:  GetterSpec{Kind: "exec", Args: []string{"echo", "no"}},
			Options: map[string]any{"expected": "yes"}, // never matches -> scores 0
		},
	}
	probe := FakeProbe{Commands: map[string]ExecResult{
		"echo ok":          {ExitCode: 0, Stdout: "ok\n"},
		"echo no":          {ExitCode: 0, Stdout: "no\n"},
		"killall cfprefsd": {ExitCode: 0},
	}}
	b := &fakeBackend{tier: TierA, probe: probe}

	tasks := []*Task{finder, settings}
	outcomes, err := Run(context.Background(), b, RunConfig{Tasks: tasks, Provider: "anthropic", Model: "claude-cu", Runs: 1})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	report, err := Aggregate(outcomes, 1, Meta{
		GeneratedAt:   "2026-05-29T00:00:00Z",
		CoveCommit:    "0afd5b1",
		HostHardware:  "Apple M-test",
		CorpusVersion: CorpusVersion(tasks),
		VerifierHash:  VerifierVersion(),
		Model:         "claude-cu",
	}, nil)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}

	if len(report.Cells) != 2 {
		t.Fatalf("got %d cells, want 2", len(report.Cells))
	}
	if len(report.Providers) != 1 {
		t.Fatalf("got %d providers, want 1", len(report.Providers))
	}
	p := report.Providers[0]
	// finder scored 1, settings scored 0 -> overall 0.5 over two tasks.
	if p.Overall != 0.5 {
		t.Fatalf("overall = %v, want 0.5", p.Overall)
	}
	if len(p.Domains) != 2 {
		t.Fatalf("got %d domain rollups, want 2 (Finder, Settings)", len(p.Domains))
	}
	for _, d := range p.Domains {
		switch d.Domain {
		case "Finder":
			if d.Mean != 1 {
				t.Fatalf("Finder mean = %v, want 1", d.Mean)
			}
		case "Settings":
			if d.Mean != 0 {
				t.Fatalf("Settings mean = %v, want 0", d.Mean)
			}
		default:
			t.Fatalf("unexpected domain %q", d.Domain)
		}
	}

	// score.json round-trips.
	var buf bytes.Buffer
	if err := report.WriteJSON(&buf); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	got, err := ReadReport(&buf)
	if err != nil {
		t.Fatalf("ReadReport: %v", err)
	}
	if got.Providers[0].Overall != 0.5 {
		t.Fatalf("round-tripped overall = %v, want 0.5", got.Providers[0].Overall)
	}
	if got.CorpusVersion != report.CorpusVersion || got.VerifierHash != report.VerifierHash {
		t.Fatalf("round-trip lost provenance: corpus %q vs %q", got.CorpusVersion, report.CorpusVersion)
	}
}
