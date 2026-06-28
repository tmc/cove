package guibench

import (
	"strings"
	"testing"
)

// distTask builds a minimal valid task with the axes Analyze reads. The getter
// kind sets the tier (exec->A, sqlite->B, accessibility->C).
func distTask(id, domain string, complexity int, kind string, infeasible bool, subsets ...string) *Task {
	g := GetterSpec{Kind: kind}
	switch kind {
	case "exec":
		g.Args = []string{"true"}
	case "sqlite":
		g.Path, g.Query = "/h.db", "SELECT 1"
	case "accessibility":
		g.App, g.Attr = "Notes", "value"
	}
	return &Task{
		ID:         id,
		Domain:     domain,
		Complexity: complexity,
		Infeasible: infeasible,
		Subset:     subsets,
		Evaluator:  Evaluator{Func: StringList{"exact_match"}, Result: g},
	}
}

func TestAnalyzeDistribution(t *testing.T) {
	tasks := []*Task{
		distTask("a", "Finder", 1, "exec", false),
		distTask("b", "Finder", 2, "sqlite", false),
		distTask("c", "Safari", 3, "accessibility", false),
		distTask("d", "Safari", 3, "exec", true),                   // infeasible
		distTask("e", "Settings", 2, "exec", false, HeldOutSubset), // held out
	}
	d := Analyze(tasks)

	if d.Total != 5 {
		t.Errorf("Total = %d, want 5", d.Total)
	}
	if d.ByDomain["Finder"] != 2 || d.ByDomain["Safari"] != 2 || d.ByDomain["Settings"] != 1 {
		t.Errorf("ByDomain = %v", d.ByDomain)
	}
	if d.ByComplexity[1] != 1 || d.ByComplexity[2] != 2 || d.ByComplexity[3] != 2 {
		t.Errorf("ByComplexity = %v", d.ByComplexity)
	}
	if d.ByTier["A"] != 3 || d.ByTier["B"] != 1 || d.ByTier["C"] != 1 {
		t.Errorf("ByTier = %v (want A:3 B:1 C:1)", d.ByTier)
	}
	if d.Infeasible != 1 {
		t.Errorf("Infeasible = %d, want 1", d.Infeasible)
	}
	if d.HeldOut != 1 {
		t.Errorf("HeldOut = %d, want 1", d.HeldOut)
	}
}

func TestCheckDistributionReportsEveryShortfall(t *testing.T) {
	// One Safari task, one Finder singleton, no infeasible, no held-out: every
	// axis of the policy should fire so M3 gets the full worklist in one run.
	d := Analyze([]*Task{
		distTask("a", "Finder", 2, "exec", false),
		distTask("b", "Safari", 2, "exec", false),
		distTask("c", "Safari", 2, "exec", false),
	})
	p := DistributionPolicy{
		MinPerDomain:       2,
		MinPerComplexity:   map[int]int{2: 5},
		MinPerTier:         map[string]int{"A": 5, "B": 1},
		MinInfeasible:      1,
		MinHeldOutFraction: 0.20,
	}
	v := d.CheckDistribution(p)
	joined := strings.Join(v, "\n")
	for _, want := range []string{
		`domain "Finder" has 1`, // singleton domain
		"complexity 2 has 3",    // below 5
		"tier A has 3",          // below 5
		"tier B has 0",          // no Tier-B task
		"infeasible has 0",      // none
		"held-out has 0",        // none
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("violations missing %q\n---\n%s", want, joined)
		}
	}
}

func TestCheckDistributionPasses(t *testing.T) {
	// A corpus that meets a small policy yields no violations.
	var tasks []*Task
	for i := range 4 {
		tasks = append(tasks, distTask("f"+string(rune('0'+i)), "Finder", 2, "exec", false))
		tasks = append(tasks, distTask("s"+string(rune('0'+i)), "Safari", 2, "exec", false))
	}
	tasks = append(tasks, distTask("inf", "Safari", 2, "exec", true))
	tasks[0].Subset = []string{HeldOutSubset}
	tasks[1].Subset = []string{HeldOutSubset}
	p := DistributionPolicy{
		MinPerDomain:       2,
		MinPerTier:         map[string]int{"A": 5},
		MinInfeasible:      1,
		MinHeldOutFraction: 0.20,
	}
	if v := Analyze(tasks).CheckDistribution(p); len(v) != 0 {
		t.Errorf("expected no violations, got:\n%s", strings.Join(v, "\n"))
	}
}

func TestMinHeldOutRoundsUp(t *testing.T) {
	// 20% of 116 = 23.2 -> 24; an exact multiple is unchanged.
	if got := minHeldOut(116, 0.20); got != 24 {
		t.Errorf("minHeldOut(116, 0.20) = %d, want 24", got)
	}
	if got := minHeldOut(100, 0.20); got != 20 {
		t.Errorf("minHeldOut(100, 0.20) = %d, want 20", got)
	}
	if got := minHeldOut(0, 0.20); got != 0 {
		t.Errorf("minHeldOut(0, 0.20) = %d, want 0", got)
	}
}

func TestZeroPolicyGatesNothing(t *testing.T) {
	d := Analyze([]*Task{distTask("a", "Finder", 1, "exec", false)})
	if v := d.CheckDistribution(DistributionPolicy{}); len(v) != 0 {
		t.Errorf("zero policy should gate nothing, got:\n%s", strings.Join(v, "\n"))
	}
}
