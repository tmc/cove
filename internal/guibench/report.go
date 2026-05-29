package guibench

import (
	"fmt"
	"io"
	"strings"
)

// RenderMarkdown writes the citable Markdown summary of a score report: the
// provenance header (the bench/README.md rule), a per-domain + overall
// success-rate matrix with providers as columns and a human-baseline column,
// and a list of variance-flagged cells for manual inspection (§9). The output
// is deterministic and stable across runs of the same report.
func RenderMarkdown(w io.Writer, r *ScoreReport) error {
	bw := &errWriter{w: w}
	bw.printf("# macOS GUI-agent benchmark results\n\n")
	renderProvenance(bw, r)
	renderOverall(bw, r)
	renderDomains(bw, r)
	renderTasks(bw, r)
	renderFlagged(bw, r)
	return bw.err
}

// renderTasks writes the per-task matrix: one row per task, one column per
// provider, each cell the task's pass@1 mean (with a "*" marker on flagged
// cells). This is the finest-grained citable view, mirroring OSWorld's
// per-task result table.
func renderTasks(w *errWriter, r *ScoreReport) {
	taskIDs := r.sortedTaskIDs()
	if len(taskIDs) == 0 {
		return
	}
	providers := r.providerNames()
	w.printf("## Per-task success rate\n\n")
	w.printf("A trailing * marks a variance-flagged cell.\n\n")
	w.printf("| Task |")
	for _, p := range providers {
		w.printf(" %s |", p)
	}
	w.printf("\n|---|")
	for range providers {
		w.printf("---|")
	}
	w.printf("\n")
	for _, id := range taskIDs {
		w.printf("| %s |", id)
		for _, p := range providers {
			c, ok := r.providerCell(p, id)
			if !ok {
				w.printf(" — |")
				continue
			}
			marker := ""
			if c.Flagged {
				marker = "*"
			}
			w.printf(" %s%s |", pct(c.Mean), marker)
		}
		w.printf("\n")
	}
	w.printf("\n")
}

// renderProvenance writes the citable metadata block. Empty fields render as
// "(not recorded)" so a reader can never mistake an unmeasured value for a
// measured one.
func renderProvenance(w *errWriter, r *ScoreReport) {
	w.printf("## Provenance\n\n")
	w.printf("| Field | Value |\n|---|---|\n")
	w.printf("| Generated | %s |\n", orNotRecorded(r.GeneratedAt))
	w.printf("| Cove commit | %s |\n", orNotRecorded(r.CoveCommit))
	w.printf("| Host hardware | %s |\n", orNotRecorded(r.HostHardware))
	w.printf("| Corpus version | %s |\n", orNotRecorded(r.CorpusVersion))
	w.printf("| Verifier hash | %s |\n", orNotRecorded(r.VerifierHash))
	w.printf("| Schema version | %s |\n", orNotRecorded(r.SchemaVersion))
	w.printf("| Runs per cell (pass@1 over N) | %d |\n", r.Runs)
	w.printf("| Tasks | %d |\n", len(r.TaskIDs))
	w.printf("| Flagged cells (>%.0f%% variance) | %d |\n\n", VarianceFlagThreshold*100, r.FlaggedCells)
}

// renderOverall writes the headline overall success-rate row per provider, plus
// the human-baseline column when present (the agent-vs-human framing, §7).
func renderOverall(w *errWriter, r *ScoreReport) {
	w.printf("## Overall success rate\n\n")
	w.printf("| Provider | Model | Overall | Tasks | Flagged |\n")
	w.printf("|---|---|---|---|---|\n")
	for _, p := range r.Providers {
		w.printf("| %s | %s | %s | %d | %d |\n",
			p.Provider, orDash(p.Model), pct(p.Overall), p.Tasks, p.FlaggedCells)
	}
	if r.HumanBaseline != nil {
		w.printf("| **human** | %s | **%s** | — | — |\n",
			orDash(r.HumanBaseline.Source), pct(r.HumanBaseline.Overall))
	}
	w.printf("\n")
}

// renderDomains writes the per-domain matrix: one row per domain, one column per
// provider, plus a human column when a baseline carries per-domain numbers.
func renderDomains(w *errWriter, r *ScoreReport) {
	if len(r.Domains) == 0 {
		return
	}
	providers := r.providerNames()
	humanByDomain := baselineDomainMap(r.HumanBaseline)
	w.printf("## Per-domain success rate\n\n")
	w.printf("| Domain |")
	for _, p := range providers {
		w.printf(" %s |", p)
	}
	if len(humanByDomain) > 0 {
		w.printf(" human |")
	}
	w.printf("\n|---|")
	for range providers {
		w.printf("---|")
	}
	if len(humanByDomain) > 0 {
		w.printf("---|")
	}
	w.printf("\n")
	for _, d := range r.Domains {
		w.printf("| %s |", d)
		for _, p := range providers {
			w.printf(" %s |", domainPct(r, p, d))
		}
		if len(humanByDomain) > 0 {
			if v, ok := humanByDomain[d]; ok {
				w.printf(" %s |", pct(v))
			} else {
				w.printf(" — |")
			}
		}
		w.printf("\n")
	}
	w.printf("\n")
}

// renderFlagged writes the flagged-cell list (provider/task, spread, scores) so
// a human can inspect each high-variance cell. It is the §9 manual-inspection
// surface; an empty list still prints a one-line all-clear.
func renderFlagged(w *errWriter, r *ScoreReport) {
	w.printf("## Variance-flagged cells\n\n")
	w.printf("Cells with score spread above %.0f%% across runs are flagged for manual inspection.\n\n", VarianceFlagThreshold*100)
	any := false
	for _, c := range r.Cells {
		if !c.Flagged {
			continue
		}
		if !any {
			w.printf("| Provider | Task | Domain | Spread | Scores |\n|---|---|---|---|---|\n")
			any = true
		}
		w.printf("| %s | %s | %s | %s | %s |\n",
			c.Provider, c.TaskID, orDash(cellDomain(c)), pct(c.Spread), formatScores(c.Scores))
	}
	if !any {
		w.printf("None.\n")
	}
	w.printf("\n")
}

// domainPct looks up a provider's mean for one domain, formatting "—" when the
// provider has no task in that domain.
func domainPct(r *ScoreReport, provider, domain string) string {
	for _, p := range r.Providers {
		if p.Provider != provider {
			continue
		}
		for _, d := range p.Domains {
			if d.Domain == domain {
				return pct(d.Mean)
			}
		}
	}
	return "—"
}

// baselineDomainMap indexes a baseline's per-domain scores by domain.
func baselineDomainMap(b *HumanBaseline) map[string]float64 {
	if b == nil || len(b.Domains) == 0 {
		return nil
	}
	out := make(map[string]float64, len(b.Domains))
	for _, d := range b.Domains {
		out[d.Domain] = d.Mean
	}
	return out
}

// formatScores renders a cell's per-run scores as a compact list.
func formatScores(scores []float64) string {
	parts := make([]string, len(scores))
	for i, s := range scores {
		parts[i] = fmt.Sprintf("%.2f", s)
	}
	return strings.Join(parts, ", ")
}

// pct formats a [0,1] rate as a one-decimal percentage.
func pct(v float64) string {
	return fmt.Sprintf("%.1f%%", v*100)
}

// orNotRecorded substitutes a visible placeholder for an empty provenance field,
// so an unrecorded value can never read as measured.
func orNotRecorded(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(not recorded)"
	}
	return s
}

// orDash substitutes an em dash for an empty optional field.
func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}

// errWriter accumulates the first write error so the renderer's many printfs
// stay readable (the bufio.Writer pattern, applied to an io.Writer).
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) printf(format string, args ...any) {
	if e.err != nil {
		return
	}
	_, e.err = fmt.Fprintf(e.w, format, args...)
}
