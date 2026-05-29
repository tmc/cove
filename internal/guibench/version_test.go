package guibench

import (
	"strings"
	"testing"
)

func TestVerifierVersionStable(t *testing.T) {
	// The hash must be deterministic across calls (no map-order dependence).
	a := VerifierVersion()
	b := VerifierVersion()
	if a != b {
		t.Fatalf("VerifierVersion not stable: %q != %q", a, b)
	}
	if !strings.HasPrefix(a, SchemaVersion+":") {
		t.Errorf("verifier version %q missing schema prefix %q", a, SchemaVersion)
	}
	// schema:hex12 — 12 hex chars after the prefix.
	hexPart := strings.TrimPrefix(a, SchemaVersion+":")
	if len(hexPart) != 12 {
		t.Errorf("verifier hex part = %q, want 12 chars", hexPart)
	}
}

func TestCorpusVersionSensitivity(t *testing.T) {
	base := []*Task{
		{ID: "a", Image: "img:1", Domain: "Finder", Evaluator: Evaluator{Func: StringList{"exact_match"}}},
		{ID: "b", Image: "img:1", Domain: "Safari", Evaluator: Evaluator{Func: StringList{"file_exists"}}},
	}
	v1 := CorpusVersion(base)

	// Reordering tasks must NOT change the version (sorted by id internally).
	reordered := []*Task{base[1], base[0]}
	if CorpusVersion(reordered) != v1 {
		t.Error("corpus version changed under task reordering")
	}

	// Changing a scored field (the evaluator func) MUST change the version.
	changed := []*Task{
		{ID: "a", Image: "img:1", Domain: "Finder", Evaluator: Evaluator{Func: StringList{"fuzzy_match"}}},
		base[1],
	}
	if CorpusVersion(changed) == v1 {
		t.Error("corpus version unchanged after evaluator func edit")
	}

	// Changing only the instruction prose must NOT change the version.
	reworded := []*Task{
		{ID: "a", Image: "img:1", Domain: "Finder", Instruction: "totally different prose", Evaluator: Evaluator{Func: StringList{"exact_match"}}},
		base[1],
	}
	if CorpusVersion(reworded) != v1 {
		t.Error("corpus version changed after instruction reword (should be prose-insensitive)")
	}
}

func TestTaskIDsAndDomains(t *testing.T) {
	tasks := []*Task{
		{ID: "z-task", Domain: "Safari"},
		{ID: "a-task", Domain: "Finder"},
		{ID: "m-task"}, // empty domain -> (none)
	}
	ids := TaskIDs(tasks)
	if len(ids) != 3 || ids[0] != "a-task" || ids[2] != "z-task" {
		t.Errorf("task ids = %v, want sorted", ids)
	}
	domains := Domains(tasks)
	want := []string{"(none)", "Finder", "Safari"}
	if strings.Join(domains, ",") != strings.Join(want, ",") {
		t.Errorf("domains = %v, want %v", domains, want)
	}
}
