package guibench_test

import (
	"fmt"
	"strings"

	"github.com/tmc/cove/internal/guibench"
)

// Example decodes a parameterized task, materializes a concrete variation from
// a seed, then scores a getter result against the variation's gold value.
func Example() {
	const taskJSON = `{
		"id": "notes-title",
		"image": "macos-base:v1",
		"instruction": "Create a note titled {TITLE}.",
		"complexity": 1,
		"schema": [
			{"name": "TITLE", "pool": ["Trip", "Recipe", "Budget"]}
		],
		"evaluator": {
			"func": "exact_match",
			"result": {"kind": "file", "path": "/Users/tmc/note-title.txt"},
			"options": {"expected": "{TITLE}"}
		}
	}`

	task, err := guibench.Decode(strings.NewReader(taskJSON))
	if err != nil {
		panic(err)
	}

	// Seed 1 deterministically picks one title; the gold value is computed from
	// the same params, so it can never go stale.
	params := task.Params(1)
	instruction := guibench.Materialize(task.Instruction, params)

	// The agent created the note; the getter would read its title off the
	// guest. Here we stand in with the chosen title to show the scoring.
	got := params["TITLE"]
	score, err := guibench.ScoreMetrics(task.Evaluator, got, "", params)
	if err != nil {
		panic(err)
	}

	fmt.Println(instruction)
	fmt.Printf("score=%.0f\n", score)
	// Output:
	// Create a note titled Budget.
	// score=1
}

// ExampleMetrics lists a registered metric and scores it directly.
func ExampleMetrics() {
	m := guibench.Metrics()["fuzzy_match"]
	score, _ := m("  Hello   World ", "hello world", nil)
	fmt.Printf("%.0f\n", score)
	// Output: 1
}

// ExampleStepBudget shows the complexity-scaled agent step budget (design 047
// §7): the budget grows with task complexity rather than using one fixed cap.
func ExampleStepBudget() {
	fmt.Println(guibench.StepBudget(0)) // floor
	fmt.Println(guibench.StepBudget(1))
	fmt.Println(guibench.StepBudget(3))
	// Output:
	// 15
	// 30
	// 60
}
