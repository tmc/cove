package guibench_test

import (
	"fmt"

	"github.com/tmc/cove/internal/guibench"
)

// ExampleRigorOf shows the verification-rigor provenance the harness publishes
// for a task: a Tier-B SQLite task runs offline, in a fresh per-task fork, and
// checkpoints the WAL before the verifier reads it.
func ExampleRigorOf() {
	task := &guibench.Task{
		ID:          "safari-history",
		Instruction: "open example.com",
		Evaluator: guibench.Evaluator{
			Func:   guibench.StringList{"sqlite_row_matches"},
			Result: guibench.GetterSpec{Kind: "sqlite", Path: "/History.db", Query: "SELECT count(*) FROM urls"},
		},
	}
	r := guibench.RigorOf(task)
	fmt.Println(r.Tier, r.EgressPolicy, r.Flushes)
	// Output:
	// B offline [cfprefsd wal]
}

// ExampleRigorSummary_Headline shows the one-line citable rigor claim rolled up
// across a corpus.
func ExampleRigorSummary_Headline() {
	byTask := map[string]guibench.TaskRigor{
		"a": guibench.RigorOf(&guibench.Task{ID: "a", Evaluator: guibench.Evaluator{
			Func:   guibench.StringList{"exact_match"},
			Result: guibench.GetterSpec{Kind: "exec", Args: []string{"echo"}},
		}}),
		"b": guibench.RigorOf(&guibench.Task{ID: "b", Evaluator: guibench.Evaluator{
			Func:   guibench.StringList{"accessibility_match"},
			Result: guibench.GetterSpec{Kind: "accessibility", App: "Notes", Attr: "value"},
		}}),
	}
	fmt.Println(guibench.SummarizeRigor(byTask).Headline())
	// Output:
	// 2 scored: 100% egress-locked; 1 Tier-A verified, 1 Tier-C verified; all tasks flush cfprefsd before read
}
