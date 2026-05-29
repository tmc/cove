package guibench

import (
	"fmt"
	"sort"
)

// Aggregate rolls raw per-attempt outcomes into a citable [ScoreReport].
//
// It groups outcomes by (provider, task) into [Cell]s, computing each cell's
// pass@1 mean over its runs, its score spread (max-min), and the >20%-variance
// flag (§9). It then rolls cells into per-domain and overall success rates per
// provider, and stamps the provenance metadata the bench/README.md rule
// requires. The result is deterministic: providers, domains, tasks, and cells
// are all emitted in a stable order regardless of outcome arrival order.
//
// runs is the intended attempts-per-cell (the --runs value); it is recorded in
// the report so a reader knows the pass@1-over-N denominator even for a cell
// that crashed on some attempts. baseline is optional (nil to omit the
// human-baseline column).
func Aggregate(outcomes []Outcome, runs int, meta Meta, baseline *HumanBaseline) (*ScoreReport, error) {
	if runs <= 0 {
		return nil, fmt.Errorf("aggregate: runs must be positive, got %d", runs)
	}
	for i, o := range outcomes {
		if o.Provider == "" {
			return nil, fmt.Errorf("aggregate: outcome %d has empty provider", i)
		}
		if o.TaskID == "" {
			return nil, fmt.Errorf("aggregate: outcome %d (%s) has empty task id", i, o.Provider)
		}
		if o.Score < 0 || o.Score > 1 {
			return nil, fmt.Errorf("aggregate: outcome %d (%s/%s) score %v out of [0,1]", i, o.Provider, o.TaskID, o.Score)
		}
	}

	cells := buildCells(outcomes)
	report := &ScoreReport{
		GeneratedAt:   meta.GeneratedAt,
		CoveCommit:    meta.CoveCommit,
		HostHardware:  meta.HostHardware,
		CorpusVersion: meta.CorpusVersion,
		VerifierHash:  meta.VerifierHash,
		SchemaVersion: SchemaVersion,
		Runs:          runs,
		Cells:         cells,
		HumanBaseline: baseline,
	}
	report.TaskIDs = distinctTaskIDs(cells)
	report.Domains = distinctDomains(cells)
	report.Providers = buildProviderScores(cells, meta.Model)
	for _, c := range cells {
		if c.Flagged {
			report.FlaggedCells++
		}
	}
	report.Rigor = summarizeCellRigor(cells)
	return report, nil
}

// summarizeCellRigor rolls the per-cell rigor into a corpus [RigorSummary],
// counting each task once regardless of how many provider cells scored it. It
// returns nil when no cell carried rigor (a hand-built fixture), so the report
// omits the section rather than printing an empty rollup.
func summarizeCellRigor(cells []Cell) *RigorSummary {
	byTask := make(map[string]TaskRigor)
	for _, c := range cells {
		if c.Rigor == nil {
			continue
		}
		if _, seen := byTask[c.TaskID]; !seen {
			byTask[c.TaskID] = *c.Rigor
		}
	}
	if len(byTask) == 0 {
		return nil
	}
	s := SummarizeRigor(byTask)
	return &s
}

// buildCells groups outcomes by (provider, task) and computes each cell's
// statistics. Within a cell, scores are ordered by run index for stable output.
func buildCells(outcomes []Outcome) []Cell {
	type key struct{ provider, task string }
	groups := make(map[key][]Outcome)
	order := make([]key, 0)
	for _, o := range outcomes {
		k := key{o.Provider, o.TaskID}
		if _, seen := groups[k]; !seen {
			order = append(order, k)
		}
		groups[k] = append(groups[k], o)
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].provider != order[j].provider {
			return order[i].provider < order[j].provider
		}
		return order[i].task < order[j].task
	})

	cells := make([]Cell, 0, len(order))
	for _, k := range order {
		g := groups[k]
		sort.Slice(g, func(i, j int) bool { return g[i].Run < g[j].Run })
		scores := make([]float64, 0, len(g))
		errors := 0
		domain := ""
		var rigor *TaskRigor
		for _, o := range g {
			scores = append(scores, o.Score)
			if o.Status == StatusError {
				errors++
			}
			if domain == "" {
				domain = o.Domain
			}
			if rigor == nil && o.Rigor != nil {
				rigor = o.Rigor
			}
		}
		m := mean(scores)
		sp := spread(scores)
		cells = append(cells, Cell{
			Provider: k.provider,
			TaskID:   k.task,
			Domain:   domain,
			Runs:     len(scores),
			Scores:   scores,
			Mean:     m,
			Spread:   sp,
			Flagged:  sp > VarianceFlagThreshold,
			Errors:   errors,
			Rigor:    rigor,
		})
	}
	return cells
}

// buildProviderScores rolls cells into per-provider overall + per-domain
// success rates, in provider-then-domain sorted order.
func buildProviderScores(cells []Cell, model string) []ProviderScore {
	byProvider := make(map[string][]Cell)
	providerOrder := make([]string, 0)
	for _, c := range cells {
		if _, seen := byProvider[c.Provider]; !seen {
			providerOrder = append(providerOrder, c.Provider)
		}
		byProvider[c.Provider] = append(byProvider[c.Provider], c)
	}
	sort.Strings(providerOrder)

	out := make([]ProviderScore, 0, len(providerOrder))
	for _, p := range providerOrder {
		pc := byProvider[p]
		means := make([]float64, 0, len(pc))
		flagged := 0
		for _, c := range pc {
			means = append(means, c.Mean)
			if c.Flagged {
				flagged++
			}
		}
		out = append(out, ProviderScore{
			Provider:     p,
			Model:        model,
			Overall:      mean(means),
			Tasks:        len(pc),
			Domains:      domainScores(pc),
			FlaggedCells: flagged,
		})
	}
	return out
}

// domainScores rolls a provider's cells into per-domain means, sorted by domain.
func domainScores(cells []Cell) []DomainScore {
	byDomain := make(map[string][]float64)
	for _, c := range cells {
		d := cellDomain(c)
		byDomain[d] = append(byDomain[d], c.Mean)
	}
	domains := make([]string, 0, len(byDomain))
	for d := range byDomain {
		domains = append(domains, d)
	}
	sort.Strings(domains)
	out := make([]DomainScore, 0, len(domains))
	for _, d := range domains {
		out = append(out, DomainScore{Domain: d, Mean: mean(byDomain[d]), Tasks: len(byDomain[d])})
	}
	return out
}

// spread returns max-min of scores, the variance proxy used for the flag. An
// empty slice has zero spread.
func spread(scores []float64) float64 {
	if len(scores) == 0 {
		return 0
	}
	lo, hi := scores[0], scores[0]
	for _, s := range scores[1:] {
		if s < lo {
			lo = s
		}
		if s > hi {
			hi = s
		}
	}
	return hi - lo
}

// cellDomain maps a cell's empty domain to the visible sentinel, matching
// [Domains].
func cellDomain(c Cell) string {
	if c.Domain == "" {
		return "(none)"
	}
	return c.Domain
}

// distinctTaskIDs returns the sorted distinct task ids across cells.
func distinctTaskIDs(cells []Cell) []string {
	seen := make(map[string]bool)
	for _, c := range cells {
		seen[c.TaskID] = true
	}
	return sortedKeys(seen)
}

// distinctDomains returns the sorted distinct domains across cells, using the
// visible sentinel for unlabeled tasks.
func distinctDomains(cells []Cell) []string {
	seen := make(map[string]bool)
	for _, c := range cells {
		seen[cellDomain(c)] = true
	}
	return sortedKeys(seen)
}
