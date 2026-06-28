package guibench

import (
	"fmt"
	"sort"
)

// HeldOutSubset is the subset tag marking a task as part of the held-out split:
// tasks reserved from the public/dev set so a leaderboard number reflects
// generalization rather than overfitting to a known corpus (OSWorld's held-out
// discipline, design 047 §9). A task carries it in its "subset" list, the same
// mechanism as "test_small".
const HeldOutSubset = "held_out"

// Distribution is the corpus-shape rollup the [CheckDistribution] gate scores:
// how the tasks spread across the axes a credible GUI-agent benchmark must
// balance — application domain, complexity (step budget), privilege tier, the
// infeasible-task share, and the held-out split. It is a pure projection of a
// task set, the corpus-shape analogue of [RigorOf] for one task.
type Distribution struct {
	// Total is the number of tasks analyzed.
	Total int
	// ByDomain counts tasks per Domain (the empty domain is keyed "").
	ByDomain map[string]int
	// ByComplexity counts tasks per complexity value (the step-budget bucket).
	ByComplexity map[int]int
	// ByTier counts tasks per privilege tier label ("A"/"B"/"C"), using the same
	// highest-getter rule as [MaxTier].
	ByTier map[string]int
	// Infeasible is how many tasks assert the agent should decline (success =
	// answering FAIL); HeldOut is how many carry the [HeldOutSubset] tag.
	Infeasible int
	HeldOut    int
}

// Analyze rolls a task set into a [Distribution]. It is pure and VM-free, so it
// runs as part of the non-Apple-Silicon CI gate over the merged corpus.
func Analyze(tasks []*Task) Distribution {
	d := Distribution{
		ByDomain:     map[string]int{},
		ByComplexity: map[int]int{},
		ByTier:       map[string]int{},
	}
	for _, t := range tasks {
		d.Total++
		d.ByDomain[t.Domain]++
		d.ByComplexity[t.Complexity]++
		d.ByTier[string(taskTier(t))]++
		if t.Infeasible {
			d.Infeasible++
		}
		if hasSubset(t, HeldOutSubset) {
			d.HeldOut++
		}
	}
	return d
}

// hasSubset reports whether the task carries the named subset tag.
func hasSubset(t *Task, name string) bool {
	for _, s := range t.Subset {
		if s == name {
			return true
		}
	}
	return false
}

// DistributionPolicy is the set of minimums [CheckDistribution] enforces. The
// zero value gates nothing (every minimum is 0), so a caller opts in by setting
// the bounds it cares about; [DefaultDistributionPolicy] is the calibrated
// policy for the headline corpus.
type DistributionPolicy struct {
	// MinPerDomain is the floor every present domain must meet, so no domain is a
	// singleton a single quirk can swing.
	MinPerDomain int
	// MinPerComplexity is the floor per complexity bucket that appears in the
	// policy's keys; a bucket not listed is not gated. Keyed by complexity value.
	MinPerComplexity map[int]int
	// MinPerTier is the floor per privilege tier label ("A"/"B"/"C"); a tier not
	// listed is not gated.
	MinPerTier map[string]int
	// MinInfeasible is the floor on infeasible tasks (robustness to unachievable
	// goals); MinHeldOutFraction is the floor on the held-out share in [0,1].
	MinInfeasible      int
	MinHeldOutFraction float64
}

// DefaultDistributionPolicy is the calibrated gate for the >=116-task headline
// corpus, sized against AndroidWorld (116 tasks) and Windows Agent Arena (154):
// every domain >=2, a complexity spread that is not all-medium, >=50 grant-free
// Tier-A tasks with >=10 Tier-B (exercising the TCC/FDA getters) and >=40
// Tier-C AX/Apple-Events tasks, >=10 infeasible tasks, and a >=20% held-out
// split (design 047 §9). The minimums sum below 116 on purpose, leaving the
// corpus room to grow without immediately breaching a ceiling.
func DefaultDistributionPolicy() DistributionPolicy {
	return DistributionPolicy{
		MinPerDomain:       2,
		MinPerComplexity:   map[int]int{1: 10, 2: 35, 3: 35, 4: 10},
		MinPerTier:         map[string]int{"A": 50, "B": 10, "C": 40},
		MinInfeasible:      10,
		MinHeldOutFraction: 0.20,
	}
}

// CheckDistribution reports every way a task set falls short of the policy, as a
// sorted list of human-readable violations (empty when the corpus passes). It is
// the structured worklist M3 corpus growth is driven by: a failing gate names
// exactly which domain, complexity, tier, or split needs more tasks, rather than
// leaving an author to guess. Returning all violations at once (not the first)
// keeps one CI run a complete to-do list.
func (d Distribution) CheckDistribution(p DistributionPolicy) []string {
	var v []string

	if p.MinPerDomain > 0 {
		for _, dom := range sortedStringCountKeys(d.ByDomain) {
			if n := d.ByDomain[dom]; n < p.MinPerDomain {
				v = append(v, fmt.Sprintf("domain %q has %d task(s), want >=%d", domainLabel(dom), n, p.MinPerDomain))
			}
		}
	}

	for _, c := range sortedIntKeys(p.MinPerComplexity) {
		want := p.MinPerComplexity[c]
		if got := d.ByComplexity[c]; got < want {
			v = append(v, fmt.Sprintf("complexity %d has %d task(s), want >=%d", c, got, want))
		}
	}

	for _, tier := range sortedStringCountKeys(p.MinPerTier) {
		want := p.MinPerTier[tier]
		if got := d.ByTier[tier]; got < want {
			v = append(v, fmt.Sprintf("tier %s has %d task(s), want >=%d", tier, got, want))
		}
	}

	if p.MinInfeasible > 0 && d.Infeasible < p.MinInfeasible {
		v = append(v, fmt.Sprintf("infeasible has %d task(s), want >=%d", d.Infeasible, p.MinInfeasible))
	}

	if p.MinHeldOutFraction > 0 {
		wantHeldOut := minHeldOut(d.Total, p.MinHeldOutFraction)
		if d.HeldOut < wantHeldOut {
			v = append(v, fmt.Sprintf("held-out has %d task(s) (%.0f%% of %d), want >=%d (%.0f%%)",
				d.HeldOut, fraction(d.HeldOut, d.Total)*100, d.Total, wantHeldOut, p.MinHeldOutFraction*100))
		}
	}

	return v
}

// minHeldOut is the held-out task count a fraction requires of total, rounded up
// so a 20%-of-116 rule needs 24, not 23.
func minHeldOut(total int, fraction float64) int {
	if total <= 0 {
		return 0
	}
	n := int(float64(total) * fraction)
	if float64(n) < float64(total)*fraction {
		n++
	}
	return n
}

func fraction(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(n) / float64(total)
}

// domainLabel renders the empty domain readably in a violation message.
func domainLabel(dom string) string {
	if dom == "" {
		return "(none)"
	}
	return dom
}

func sortedStringCountKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedIntKeys(m map[int]int) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}
