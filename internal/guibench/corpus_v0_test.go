package guibench

import (
	"strings"
	"testing"
)

// corpusV0Dir is the seed corpus shipped in slice 4 (design 047 §9). It loads
// and validates without a VM, so this test is part of the gate even on
// non-Apple-Silicon CI.
const corpusV0Dir = "testdata/corpus-v0"

func loadCorpusV0(t *testing.T) []*Task {
	t.Helper()
	tasks, err := Load(corpusV0Dir)
	if err != nil {
		t.Fatalf("Load(%s): %v", corpusV0Dir, err)
	}
	return tasks
}

func TestCorpusV0LoadsAndValidates(t *testing.T) {
	tasks := loadCorpusV0(t)
	// The seed corpus is 10–20 tasks (design 047 §9 slice 4).
	if len(tasks) < 10 || len(tasks) > 20 {
		t.Fatalf("corpus has %d tasks, want 10–20", len(tasks))
	}
	// Load already validates each task (unknown metric, missing conj, unknown
	// getter kind); reaching here means every record is structurally sound.
}

func TestCorpusV0MetricsRegistered(t *testing.T) {
	known := metricNames()
	for _, task := range loadCorpusV0(t) {
		for _, name := range task.Evaluator.Func {
			if !known[name] {
				t.Errorf("task %s uses unregistered metric %q", task.ID, name)
			}
		}
	}
}

func TestCorpusV0TiersDeclared(t *testing.T) {
	valid := map[Tier]bool{TierA: true, TierB: true, TierC: true}
	for _, task := range loadCorpusV0(t) {
		tier := task.Evaluator.Result.Tier()
		if !valid[tier] {
			t.Errorf("task %s: result getter kind %q has undeclared tier %q",
				task.ID, task.Evaluator.Result.Kind, tier)
		}
		if task.Evaluator.Expected != nil {
			if et := task.Evaluator.Expected.Tier(); !valid[et] {
				t.Errorf("task %s: expected getter kind %q has undeclared tier %q",
					task.ID, task.Evaluator.Expected.Kind, et)
			}
		}
	}
}

func TestCorpusV0NoAppleIDTasks(t *testing.T) {
	// v1 excludes iCloud/Keychain/Apple-ID tasks (shared-SEP hazard, design 047
	// §6). Guard against a future edit reintroducing one by scanning the
	// instruction for the forbidden surfaces.
	forbidden := []string{"icloud", "keychain", "apple id", "apple-id", "find my", "fairplay"}
	for _, task := range loadCorpusV0(t) {
		lower := strings.ToLower(task.Instruction)
		for _, f := range forbidden {
			if strings.Contains(lower, f) {
				t.Errorf("task %s instruction mentions forbidden surface %q (shared-SEP hazard, §6)", task.ID, f)
			}
		}
	}
}

func TestCorpusV0SelfCheckable(t *testing.T) {
	// Every feasible task must carry a known-good solution (so slice 4's
	// self-check can run it); every infeasible task must declare infeasible and
	// carry none.
	for _, task := range loadCorpusV0(t) {
		if err := task.CheckSelfCheckable(); err != nil {
			t.Errorf("task %s is not self-checkable: %v", task.ID, err)
		}
		if task.Infeasible && len(task.Solution) != 0 {
			t.Errorf("task %s is infeasible but carries a solution", task.ID)
		}
	}
}

func TestCorpusV0Parameterized(t *testing.T) {
	for _, task := range loadCorpusV0(t) {
		const seed = 3
		a := task.Params(seed)
		b := task.Params(seed)
		if len(a) != len(b) {
			t.Errorf("task %s: Params not deterministic (len %d vs %d)", task.ID, len(a), len(b))
			continue
		}
		for k, v := range a {
			if b[k] != v {
				t.Errorf("task %s: Params(%d)[%q] not deterministic: %q vs %q", task.ID, seed, k, v, b[k])
			}
		}
		// Every schema param must resolve to a non-empty value (an empty pool or
		// a missing derive key is an authoring bug).
		for _, p := range task.Schema {
			if a[p.Name] == "" {
				t.Errorf("task %s: param %q resolved to empty for seed %d", task.ID, p.Name, seed)
			}
		}
	}
}

func TestCorpusV0DomainCoverage(t *testing.T) {
	tasks := loadCorpusV0(t)
	want := []string{"Finder", "Safari", "Settings", "Notes", "Preview", "Terminal"}
	have := make(map[string]bool)
	for _, task := range tasks {
		if task.Domain == "" {
			t.Errorf("task %s has no domain", task.ID)
		}
		have[task.Domain] = true
	}
	for _, d := range want {
		if !have[d] {
			t.Errorf("corpus is missing the %q domain", d)
		}
	}
}

func TestCorpusV0Versioned(t *testing.T) {
	tasks := loadCorpusV0(t)
	// A corpus version is recordable and stable across loads (design 047 §7).
	v1 := CorpusVersion(tasks)
	v2 := CorpusVersion(tasks)
	if v1 != v2 || v1 == "" {
		t.Fatalf("CorpusVersion unstable or empty: %q vs %q", v1, v2)
	}
	if len(TaskIDs(tasks)) != len(tasks) {
		t.Fatalf("TaskIDs count mismatch")
	}
}

func TestCorpusV0TestSmallSubset(t *testing.T) {
	tasks := loadCorpusV0(t)
	small, err := SelectSubset(tasks, SubsetTestSmall)
	if err != nil {
		t.Fatalf("test_small subset: %v", err)
	}
	if len(small) == 0 || len(small) >= len(tasks) {
		t.Fatalf("test_small has %d of %d tasks; want a non-empty proper subset", len(small), len(tasks))
	}
}
