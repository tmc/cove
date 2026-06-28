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

// TestCorpusDistribution gates the merged corpus shape against the headline
// [DefaultDistributionPolicy]: every domain >=2, the complexity spread, the
// per-tier floors, >=10 infeasible, and a >=20% held-out split. The corpus
// reached the >=116-task target that satisfies this policy, so the gate is a
// hard assert — any change that unbalances the corpus (a domain regressing to a
// singleton, the held-out share dropping below 20%, a tier falling under its
// floor) fails CI with the precise shortfall. It always logs the full
// distribution so a reader sees the corpus shape at a glance.
func TestCorpusDistribution(t *testing.T) {
	d := Analyze(loadMergedCorpus(t))
	t.Logf("corpus: %d tasks, %d infeasible, %d held-out", d.Total, d.Infeasible, d.HeldOut)
	t.Logf("by tier:       %v", d.ByTier)
	t.Logf("by complexity: %v", d.ByComplexity)
	t.Logf("by domain:     %v", d.ByDomain)

	if v := d.CheckDistribution(DefaultDistributionPolicy()); len(v) > 0 {
		t.Errorf("corpus does not meet DefaultDistributionPolicy:\n  %s", strings.Join(v, "\n  "))
	}
}
