package guibench

import (
	"strings"
	"testing"
)

// corpusDirs is the full shipped corpus: v0 (seed), v1 (fidelity-layer
// expansion), and adapted (intent-ported from prior GUI-agent benchmarks). The
// distribution gate scores their union, since a leaderboard runs the merged set.
var corpusDirs = []string{
	"testdata/corpus-v0",
	"testdata/corpus-v1",
	"testdata/corpus-adapted",
}

// loadMergedCorpus loads and validates every shipped corpus task. Like the
// per-corpus load tests it needs no VM, so it runs on the non-Apple-Silicon
// gate.
func loadMergedCorpus(t *testing.T) []*Task {
	t.Helper()
	var all []*Task
	for _, dir := range corpusDirs {
		tasks, err := Load(dir)
		if err != nil {
			t.Fatalf("Load(%s): %v", dir, err)
		}
		all = append(all, tasks...)
	}
	return all
}

// TestCorpusDistribution gates the merged corpus shape. It always logs the full
// distribution and the violations against [DefaultDistributionPolicy] — the M3
// growth worklist (which domain/complexity/tier/split still needs tasks). It
// fails only on REGRESSION below the current achievable floor, so the gate
// protects the corpus's balance as it grows without blocking on the not-yet-met
// >=116 headline target. Tighten currentFloor toward DefaultDistributionPolicy
// as M3 lands tasks; flip the headline check from t.Log to a hard assert once
// the corpus reaches it.
func TestCorpusDistribution(t *testing.T) {
	d := Analyze(loadMergedCorpus(t))
	t.Logf("corpus: %d tasks, %d infeasible, %d held-out", d.Total, d.Infeasible, d.HeldOut)
	t.Logf("by tier:       %v", d.ByTier)
	t.Logf("by complexity: %v", d.ByComplexity)
	t.Logf("by domain:     %v", d.ByDomain)

	// The headline target is not yet met; surface its remaining worklist without
	// failing, so M3 has a precise to-do list every CI run.
	if headline := d.CheckDistribution(DefaultDistributionPolicy()); len(headline) > 0 {
		t.Logf("M3 growth worklist vs DefaultDistributionPolicy (%d items):\n  %s",
			len(headline), strings.Join(headline, "\n  "))
	} else {
		t.Log("corpus meets DefaultDistributionPolicy; consider flipping the headline check to a hard assert")
	}

	// Regression guard: the corpus must not drop below the balance it already
	// has. These floors are <= the current real distribution; raising them as M3
	// adds tasks is how the gate ratchets toward the headline policy.
	currentFloor := DistributionPolicy{
		MinPerDomain:     1, // every domain present keeps at least one task
		MinPerComplexity: map[int]int{1: 6, 2: 27, 3: 25, 4: 7},
		MinPerTier:       map[string]int{"A": 38, "B": 3, "C": 24},
		MinInfeasible:    4,
	}
	if v := d.CheckDistribution(currentFloor); len(v) > 0 {
		t.Errorf("corpus regressed below its current distribution floor:\n  %s", strings.Join(v, "\n  "))
	}
}
