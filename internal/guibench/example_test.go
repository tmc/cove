package guibench_test

import (
	"fmt"
	"reflect"
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

// ExampleMetrics_accessibilityMatch scores the AX dump from the accessibility
// getter (Dump=true): it asserts a node with a given role exists and its
// AXValue matches, the macOS-AX analogue of OSWorld's check_accessibility_tree.
func ExampleMetrics_accessibilityMatch() {
	// The accessibility getter with Dump set emits the front window's AX subtree
	// as XML; here we stand in with a Notes window holding one text area.
	dump := `<ax app="Notes">` +
		`<node role="AXWindow" title="Notes" identifier="" value="">` +
		`<node role="AXTextArea" title="Body" identifier="" value="Buy milk" />` +
		`</node>` +
		`</ax>`

	m := guibench.Metrics()["accessibility_match"]
	score, _ := m(dump, "", map[string]any{"role": "AXTextArea", "value": "Buy milk"})
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

// ExampleMetrics_rowsAddedIntegrity scores a before/after whole-table snapshot:
// the agent must add the target row AND leave every other ("noise") row intact.
// Deleting the noise rows and re-creating only the target — the false positive a
// single-point read accepts — scores 0 (AndroidWorld
// validate_rows_addition_integrity).
func ExampleMetrics_rowsAddedIntegrity() {
	m := guibench.Metrics()["rows_added_integrity"]

	const before = "2|Groceries\n3|Standup agenda"
	const target = "4|Trip itinerary"

	// Target added, noise intact.
	ok, _ := m(before+"\n"+target, before, map[string]any{"target": target})
	// Noise wiped, only the target left behind.
	bad, _ := m(target, before, map[string]any{"target": target})

	fmt.Printf("intact=%.0f wiped=%.0f\n", ok, bad)
	// Output: intact=1 wiped=0
}

// ExampleNoiseRows seeds deterministic distractor rows so an integrity task does
// not ship a table with only the target present. The same seed always yields the
// same rows, keeping the gold answer self-consistent across runs (design 047
// §10).
func ExampleNoiseRows() {
	pools := [][]string{
		{"1", "2", "3"},
		{"Groceries", "Standup agenda", "Reading list"},
	}
	first := guibench.NoiseRows(7, 2, pools)
	again := guibench.NoiseRows(7, 2, pools)
	fmt.Printf("rows=%d deterministic=%t\n", len(first), reflect.DeepEqual(first, again))
	// Output: rows=2 deterministic=true
}
