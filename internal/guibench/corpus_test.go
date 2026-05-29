package guibench_test

import (
	"strings"
	"testing"

	"github.com/tmc/cove/internal/guibench"
)

// exampleCorpusDir is the shipped example external corpus (design 047 §9
// slice 7). It demonstrates the task schema end-to-end and must load and
// validate clean against the slice-1 loader, with no VM. The path is relative
// to this package (internal/guibench) up to the repo root.
const exampleCorpusDir = "../../docs/benchmarks/guibench/example-corpus"

// TestExampleCorpusLoads asserts the example corpus loads and validates without
// a VM, so a third party copying it as a template starts from a known-good
// baseline (the "example corpus validates against the loader" gate).
func TestExampleCorpusLoads(t *testing.T) {
	tasks, err := guibench.Load(exampleCorpusDir)
	if err != nil {
		t.Fatalf("load example corpus: %v", err)
	}
	if len(tasks) == 0 {
		t.Fatalf("example corpus is empty; expected the shipped demonstration tasks")
	}
	for _, task := range tasks {
		if err := task.Validate(); err != nil {
			t.Errorf("task %s: validate: %v", task.ID, err)
		}
	}
}

// TestExampleCorpusTiers asserts every example task uses only Tier-A getters,
// so it runs on a fresh fork with no FDA/Apple-Events grant (design 047 §5).
// A third party can run the example corpus before building a pre-granted image.
func TestExampleCorpusTiers(t *testing.T) {
	tasks, err := guibench.Load(exampleCorpusDir)
	if err != nil {
		t.Fatalf("load example corpus: %v", err)
	}
	tierAKinds := map[string]bool{"exec": true, "file": true, "defaults": true, "screen_ocr": true}
	for _, task := range tasks {
		kinds := []string{task.Evaluator.Result.Kind}
		if task.Evaluator.Expected != nil {
			kinds = append(kinds, task.Evaluator.Expected.Kind)
		}
		for _, k := range kinds {
			if !tierAKinds[k] {
				t.Errorf("task %s: getter kind %q is not Tier-A; the example corpus must run without grants", task.ID, k)
			}
		}
	}
}

// TestExampleCorpusParameterizesDeterministically asserts the parameterized
// example tasks materialize the same variation for the same seed (design 047
// §10 anti-memorization) — the property a third party's verifier relies on.
func TestExampleCorpusParameterizesDeterministically(t *testing.T) {
	tasks, err := guibench.Load(exampleCorpusDir)
	if err != nil {
		t.Fatalf("load example corpus: %v", err)
	}
	for _, task := range tasks {
		if len(task.Schema) == 0 {
			continue
		}
		const seed = 7
		first := task.Params(seed)
		second := task.Params(seed)
		for name, want := range first {
			if got := second[name]; got != want {
				t.Errorf("task %s: param %s not deterministic: %q vs %q", task.ID, name, want, got)
			}
		}
		// A placeholder named in the schema must materialize out of the
		// instruction, so an authored template never ships a stray {NAME}.
		inst := guibench.Materialize(task.Instruction, first)
		for _, p := range task.Schema {
			value, ok := first[p.Name]
			if !ok || value == "" {
				continue
			}
			if !strings.Contains(task.Instruction, "{"+p.Name+"}") {
				continue
			}
			if !strings.Contains(inst, value) {
				t.Errorf("task %s: instruction did not materialize {%s}", task.ID, p.Name)
			}
		}
	}
}
