package guibench

import (
	"strings"
	"testing"
)

// corpusAdaptedDir is the intent-ported third-party corpus (the portable subset
// of OSWorld/WebArena/cua-bench re-expressed against native macOS, see
// docs/benchmarks/guibench/adapters.md). Like corpus-v0 it loads and validates
// without a VM, so this test is part of the gate even on non-Apple-Silicon CI.
const corpusAdaptedDir = "testdata/corpus-adapted"

func loadCorpusAdapted(t *testing.T) []*Task {
	t.Helper()
	tasks, err := Load(corpusAdaptedDir)
	if err != nil {
		t.Fatalf("Load(%s): %v", corpusAdaptedDir, err)
	}
	return tasks
}

func TestCorpusAdaptedLoadsAndValidates(t *testing.T) {
	tasks := loadCorpusAdapted(t)
	// A small adapted batch (10–20 tasks).
	if len(tasks) < 10 || len(tasks) > 20 {
		t.Fatalf("adapted corpus has %d tasks, want 10–20", len(tasks))
	}
	// Load already validates each task (unknown metric, missing conj, unknown
	// getter kind); reaching here means every record is structurally sound.
}

// TestCorpusAdaptedEveryTaskCitesUpstream asserts the load-bearing adapter
// convention: every adapted task's Source parses as an adapter citation naming
// a real upstream benchmark, a verbatim upstream id, and a valid adaptation
// mode. A cove-original task would carry no "adapted:" tag — this corpus is
// adapted-only, so each one must.
func TestCorpusAdaptedEveryTaskCitesUpstream(t *testing.T) {
	for _, task := range loadCorpusAdapted(t) {
		if !task.IsAdapted() {
			t.Errorf("task %s: source does not follow the adapter convention: %q", task.ID, task.Source)
			continue
		}
		p, err := task.Provenance()
		if err != nil {
			t.Errorf("task %s: %v", task.ID, err)
			continue
		}
		if p.Benchmark == "" || p.UpstreamID == "" {
			t.Errorf("task %s: provenance missing benchmark or upstream id: %+v", task.ID, p)
		}
		switch p.Mode {
		case ModePort, ModeIntent:
		default:
			t.Errorf("task %s: invalid adaptation mode %q", task.ID, p.Mode)
		}
	}
}

// TestCorpusAdaptedNoForeignAppInstall guards the adapter trap (design 047 §16):
// an adapted task must never require installing a foreign desktop app on macOS.
// We scan the instruction and every setup/solution step for the foreign-app
// names that would mean "run that app on macOS" rather than re-express the
// intent natively.
func TestCorpusAdaptedNoForeignAppInstall(t *testing.T) {
	foreign := []string{"libreoffice", "gimp", "vlc", "thunderbird", "microsoft office", "ms office", "google chrome", "chromium", "wordpad", "notepad++"}
	for _, task := range loadCorpusAdapted(t) {
		hay := []string{strings.ToLower(task.Instruction)}
		for _, s := range task.Config {
			hay = append(hay, strings.ToLower(strings.Join(s.Args, " ")))
		}
		for _, s := range task.Solution {
			hay = append(hay, strings.ToLower(strings.Join(s.Args, " ")))
		}
		for _, h := range hay {
			for _, f := range foreign {
				if strings.Contains(h, f) {
					t.Errorf("task %s mentions foreign app %q in an executable instruction/step (adapter trap, §16): %q", task.ID, f, h)
				}
			}
		}
	}
}

// TestCorpusAdaptedIntentTasksAreNative asserts intent-re-expressed tasks target
// an Apple-native app domain, never the foreign app they descend from. A port
// task may keep a cross-platform domain (Terminal/Finder/Safari/Settings).
func TestCorpusAdaptedIntentTasksAreNative(t *testing.T) {
	appleNative := map[string]bool{
		"Numbers": true, "Pages": true, "Mail": true, "Reminders": true,
		"Notes": true, "Calendar": true, "Keynote": true, "Preview": true,
	}
	for _, task := range loadCorpusAdapted(t) {
		p, err := task.Provenance()
		if err != nil {
			t.Fatalf("task %s: %v", task.ID, err)
		}
		if p.Mode == ModeIntent && !appleNative[task.Domain] {
			t.Errorf("task %s is mode=intent but domain %q is not an Apple-native app; intent re-expression must target a native app", task.ID, task.Domain)
		}
	}
}

func TestCorpusAdaptedMetricsRegistered(t *testing.T) {
	known := metricNames()
	for _, task := range loadCorpusAdapted(t) {
		for _, name := range task.Evaluator.Func {
			if !known[name] {
				t.Errorf("task %s uses unregistered metric %q", task.ID, name)
			}
		}
	}
}

func TestCorpusAdaptedTiersDeclared(t *testing.T) {
	valid := map[Tier]bool{TierA: true, TierB: true, TierC: true}
	for _, task := range loadCorpusAdapted(t) {
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

func TestCorpusAdaptedNoAppleIDTasks(t *testing.T) {
	// v1 excludes iCloud/Keychain/Apple-ID tasks (shared-SEP hazard, design 047
	// §6), same as corpus-v0.
	forbidden := []string{"icloud", "keychain", "apple id", "apple-id", "find my", "fairplay"}
	for _, task := range loadCorpusAdapted(t) {
		lower := strings.ToLower(task.Instruction)
		for _, f := range forbidden {
			if strings.Contains(lower, f) {
				t.Errorf("task %s instruction mentions forbidden surface %q (shared-SEP hazard, §6)", task.ID, f)
			}
		}
	}
}

func TestCorpusAdaptedSelfCheckable(t *testing.T) {
	// Every feasible task must carry a known-good solution; every infeasible task
	// must declare infeasible and carry none.
	for _, task := range loadCorpusAdapted(t) {
		if err := task.CheckSelfCheckable(); err != nil {
			t.Errorf("task %s is not self-checkable: %v", task.ID, err)
		}
		if task.Infeasible && len(task.Solution) != 0 {
			t.Errorf("task %s is infeasible but carries a solution", task.ID)
		}
	}
}

func TestCorpusAdaptedParameterized(t *testing.T) {
	for _, task := range loadCorpusAdapted(t) {
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
		for _, p := range task.Schema {
			if a[p.Name] == "" {
				t.Errorf("task %s: param %q resolved to empty for seed %d", task.ID, p.Name, seed)
			}
		}
	}
}

func TestCorpusAdaptedVersioned(t *testing.T) {
	tasks := loadCorpusAdapted(t)
	v1 := CorpusVersion(tasks)
	v2 := CorpusVersion(tasks)
	if v1 != v2 || v1 == "" {
		t.Fatalf("CorpusVersion unstable or empty: %q vs %q", v1, v2)
	}
	if len(TaskIDs(tasks)) != len(tasks) {
		t.Fatalf("TaskIDs count mismatch")
	}
}

func TestCorpusAdaptedTestSmallSubset(t *testing.T) {
	tasks := loadCorpusAdapted(t)
	small, err := SelectSubset(tasks, SubsetTestSmall)
	if err != nil {
		t.Fatalf("test_small subset: %v", err)
	}
	if len(small) == 0 || len(small) >= len(tasks) {
		t.Fatalf("test_small has %d of %d tasks; want a non-empty proper subset", len(small), len(tasks))
	}
}

// TestCorpusAdaptedDoNotTrackNoOpInvariant proves the slice-4 no-op invariant
// for the adapted defaults-toggle task (safari-do-not-track) across the seed
// range that materializes both ACTION values: config writes the BASELINE
// (derived as the opposite of the goal), so the no-op never accidentally equals
// the goal. It reuses the stateful defaultsGuest from the seed-invariant test.
func TestCorpusAdaptedDoNotTrackNoOpInvariant(t *testing.T) {
	tasks := loadCorpusAdapted(t)
	var task *Task
	for _, x := range tasks {
		if x.ID == "safari-do-not-track" {
			task = x
			break
		}
	}
	if task == nil {
		t.Fatal("adapted corpus is missing safari-do-not-track")
	}
	for seed := uint64(1); seed <= 4; seed++ {
		env := &defaultsEnv{}
		r := SelfCheck(env, task, seed)
		if r.Err != nil {
			t.Fatalf("seed %d: self-check error: %v", seed, r.Err)
		}
		if r.Good != 1 {
			t.Errorf("seed %d: good run scored %v, want 1 (gold solution must satisfy the verifier)", seed, r.Good)
		}
		if r.NoOp != 0 {
			t.Errorf("seed %d: NO-OP scored %v, want 0 (config baseline must differ from the goal for this materialized value)", seed, r.NoOp)
		}
	}
}
