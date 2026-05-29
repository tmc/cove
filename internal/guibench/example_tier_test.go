package guibench_test

import (
	"fmt"

	"github.com/tmc/cove/internal/guibench"
)

// ExampleGetterSpec_Tier classifies getters by the TCC grant a fresh fork must
// carry (design 047 §5): Tier A needs no grant, Tier B needs Full Disk Access,
// Tier C needs Apple Events plus Accessibility.
func ExampleGetterSpec_Tier() {
	for _, kind := range []string{"file", "sqlite", "accessibility"} {
		t := guibench.GetterSpec{Kind: kind}.Tier()
		fmt.Printf("%s: tier %s (%s)\n", kind, t, t.Grant())
	}
	// Output:
	// file: tier A (none)
	// sqlite: tier B (Full Disk Access)
	// accessibility: tier C (Apple Events + Accessibility)
}

// ExampleMaxTier reports the grant level a corpus requires, which the base
// image must carry before it is saved (design 047 §5, §12).
func ExampleMaxTier() {
	tasks := []*guibench.Task{
		{ID: "a", Evaluator: guibench.Evaluator{Func: guibench.StringList{"exact_match"}, Result: guibench.GetterSpec{Kind: "defaults"}}},
		{ID: "b", Evaluator: guibench.Evaluator{Func: guibench.StringList{"sqlite_row_matches"}, Result: guibench.GetterSpec{Kind: "sqlite"}}},
	}
	t := guibench.MaxTier(tasks)
	fmt.Printf("corpus needs tier %s: %s\n", t, t.Grant())
	// Output:
	// corpus needs tier B: Full Disk Access
}

// ExampleGetterSpec_Get_applescript shows the Tier-C AppleScript getter reading
// the active Safari tab URL through a one-shot osascript (design 047 §13). A
// FakeProbe stands in for the live guest so the shape is testable without a VM.
func ExampleGetterSpec_Get_applescript() {
	probe := guibench.FakeProbe{
		Commands: map[string]guibench.ExecResult{
			"osascript -e tell application \"Safari\" to return URL of current tab of front window": {Stdout: "https://example.com/\n"},
		},
	}
	spec := guibench.GetterSpec{
		Kind:   "applescript",
		Script: "tell application \"Safari\" to return URL of current tab of front window",
	}
	url, err := spec.Get(probe, nil)
	if err != nil {
		panic(err)
	}
	fmt.Println(url)
	// Output: https://example.com/
}
