package guibench

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
)

// VarianceFlagThreshold is the per-cell score-spread above which a cell is
// flagged for manual inspection. Model behavior is non-deterministic even in a
// deterministic VM, so a wide spread across runs of the same provider×task
// means the number is not yet trustworthy (design 047 §8/§9: "any
// >20%-variance cell flagged for manual inspection").
const VarianceFlagThreshold = 0.20

// Outcome is one scored attempt: a single provider running a single task once.
// It is the raw row the runner emits per (provider, task, run); aggregation
// rolls a set of Outcomes into a [ScoreReport]. Score is in [0,1]. A task that
// crashed scores 0 with Status "error" and an Error message (AndroidWorld's
// try/except discipline, §9): a crash is a zero, not a dropped row.
type Outcome struct {
	Provider string  `json:"provider"`
	Model    string  `json:"model,omitempty"`
	TaskID   string  `json:"task_id"`
	Domain   string  `json:"domain,omitempty"`
	Run      int     `json:"run"`             // 0-based run index
	Score    float64 `json:"score"`           // [0,1]
	Status   string  `json:"status"`          // "scored" | "error"
	Error    string  `json:"error,omitempty"` // populated when Status == "error"
	// Rigor is the verification-rigor provenance the runner enforced for this
	// task (isolation, egress lockdown, privilege tier, pre-read flushes; design
	// 047 §16). It is a pure projection of the task, stamped by the runner so the
	// citable report can publish it as first-class columns rather than leaving a
	// reader to assume the discipline. Zero value when an outcome was synthesized
	// without a task (e.g. a hand-built fixture).
	Rigor *TaskRigor `json:"rigor,omitempty"`
}

// Outcome status values.
const (
	StatusScored = "scored"
	StatusError  = "error"
)

// Cell is the aggregate of every run of one provider against one task. Mean is
// the pass@1 success rate over Runs attempts; Spread is max-min across runs
// (the variance proxy §9 names); Flagged is Spread > VarianceFlagThreshold. A
// flagged cell is not excluded — it is reported and marked so a human inspects
// it (the design rejects re-rolling).
type Cell struct {
	Provider string    `json:"provider"`
	TaskID   string    `json:"task_id"`
	Domain   string    `json:"domain,omitempty"`
	Runs     int       `json:"runs"`
	Scores   []float64 `json:"scores"`
	Mean     float64   `json:"mean"`
	Spread   float64   `json:"spread"`
	Flagged  bool      `json:"flagged"`
	Errors   int       `json:"errors,omitempty"` // runs that crashed (counted as 0)
	// Rigor is the verification-rigor provenance of the task this cell scored
	// (design 047 §16), carried up from the cell's outcomes so the per-task
	// matrix can publish isolation, egress, tier, and flush columns. Nil when the
	// outcomes carried no rigor (a hand-built fixture).
	Rigor *TaskRigor `json:"rigor,omitempty"`
}

// DomainScore is a provider's mean success rate over all tasks in one domain.
type DomainScore struct {
	Domain string  `json:"domain"`
	Mean   float64 `json:"mean"`
	Tasks  int     `json:"tasks"`
}

// ProviderScore rolls one provider's whole matrix: overall success rate, the
// per-domain breakdown, and how many of its cells were variance-flagged.
type ProviderScore struct {
	Provider     string        `json:"provider"`
	Model        string        `json:"model,omitempty"`
	Overall      float64       `json:"overall"`
	Tasks        int           `json:"tasks"`
	Domains      []DomainScore `json:"domains"`
	FlaggedCells int           `json:"flagged_cells"`
}

// ScoreReport is the citable score.json: the metadata the bench/README.md rule
// requires (host hardware, cove commit, timestamp, corpus + verifier versions,
// task ids), plus the per-cell results and per-provider rollups. The
// HumanBaseline slot carries the agent-vs-human framing (§7/§11); it is
// reported alongside the agents but never derived from agent runs.
type ScoreReport struct {
	GeneratedAt   string          `json:"generated_at"`
	CoveCommit    string          `json:"cove_commit"`
	HostHardware  string          `json:"host_hardware"`
	CorpusVersion string          `json:"corpus_version"`
	VerifierHash  string          `json:"verifier_hash"`
	SchemaVersion string          `json:"schema_version"`
	Runs          int             `json:"runs"` // attempts per cell (pass@1 over N)
	TaskIDs       []string        `json:"task_ids"`
	Domains       []string        `json:"domains"`
	Providers     []ProviderScore `json:"providers"`
	Cells         []Cell          `json:"cells"`
	HumanBaseline *HumanBaseline  `json:"human_baseline,omitempty"`
	FlaggedCells  int             `json:"flagged_cells"`
	// Rigor is the corpus-level verification-rigor rollup (design 047 §16): the
	// one-line "100% egress-locked, N Tier-C AX-verified, all persisted-state
	// getters flush before read" claim, derived from the per-task rigor carried
	// on the cells. Nil when no cell carried rigor.
	Rigor *RigorSummary `json:"rigor,omitempty"`
	// Calibration is the corpus-level verifier-calibration rollup (design 047 §9
	// slice 4): how many scored tasks have a verifier that scores its own gold
	// solution 1 and a no-op 0. It is the "is the validator correct" claim that
	// underwrites every agent score in this report. Nil when the report was not
	// accompanied by a self-check pass (e.g. scores aggregated without one).
	Calibration *CalibrationSummary `json:"calibration,omitempty"`
}

// HumanBaseline is the human success-rate column reported alongside the agents
// (OSWorld human 72.4%, WebArena human 78.2%, §7). It is supplied by the
// operator, not measured by the harness, so Source records its provenance.
type HumanBaseline struct {
	Overall float64       `json:"overall"`
	Domains []DomainScore `json:"domains,omitempty"`
	Source  string        `json:"source,omitempty"`
}

// Meta carries the run-provenance fields a [ScoreReport] must record. It is
// passed to [Aggregate] so the citable metadata is filled in one place rather
// than scattered through the runner.
type Meta struct {
	GeneratedAt   string
	CoveCommit    string
	HostHardware  string
	CorpusVersion string
	VerifierHash  string
	Model         string // model id, when uniform across providers; per-provider models override
}

// WriteJSON encodes the report as indented JSON to w.
func (r *ScoreReport) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		return fmt.Errorf("write score report: %w", err)
	}
	return nil
}

// ReadReport decodes a score.json from r.
func ReadReport(r io.Reader) (*ScoreReport, error) {
	dec := json.NewDecoder(r)
	var rep ScoreReport
	if err := dec.Decode(&rep); err != nil {
		return nil, fmt.Errorf("read score report: %w", err)
	}
	return &rep, nil
}

// providerCell finds the cell for a provider+task in a report, used by the
// renderer to lay out the matrix. The second return is false when absent.
func (r *ScoreReport) providerCell(provider, taskID string) (Cell, bool) {
	for _, c := range r.Cells {
		if c.Provider == provider && c.TaskID == taskID {
			return c, true
		}
	}
	return Cell{}, false
}

// providerNames returns the report's providers in report order.
func (r *ScoreReport) providerNames() []string {
	out := make([]string, 0, len(r.Providers))
	for _, p := range r.Providers {
		out = append(out, p.Provider)
	}
	return out
}

// sortedTaskIDs returns the report's task ids in sorted order.
func (r *ScoreReport) sortedTaskIDs() []string {
	out := make([]string, len(r.TaskIDs))
	copy(out, r.TaskIDs)
	sort.Strings(out)
	return out
}
