package guibench

import (
	"context"
	"fmt"
	"sort"
)

// RunConfig configures one scoring run of a corpus against one provider
// (design 047 §9 slice 2). The runner forks a fresh hermetic environment per
// task through Backend, runs the task's setup, drives the agent, flushes async
// OS state, reads end-state through the session's [Probe], scores it with the
// task's metric, and discards the environment — one fresh fork per task, never
// reused (design 047 §6, §12).
type RunConfig struct {
	// Tasks is the (already subset-selected) corpus to score. The runner runs
	// every task in this slice.
	Tasks []*Task
	// Provider is the agent provider label recorded on each [Outcome] (e.g.
	// "anthropic"). The Backend's RunAgent decides how the label maps to a real
	// agent; the runner only records it.
	Provider string
	// Model is the optional model id recorded alongside Provider.
	Model string
	// Runs is the attempts-per-task (pass@1 over N, design 047 §7). Each task is
	// run this many times against a fresh fork; <=0 is treated as 1.
	Runs int
	// Image overrides the per-task Image when set, so an operator can point a
	// whole corpus at one base image without editing every task.
	Image string
	// ParamSeed seeds the deterministic parameter materialization. The same seed
	// yields the same task variations, so a re-run is reproducible.
	ParamSeed uint64
	// Checkpoint, when non-nil, persists each completed [Outcome] so an
	// interrupted run resumes and skips already-scored (task, run) cells.
	Checkpoint *Checkpoint
	// Observer, when non-nil, receives each outcome as it is scored, for live
	// progress reporting. It must not block.
	Observer func(Outcome)
}

// Run scores every task in cfg.Tasks against cfg.Provider and returns the raw
// per-attempt outcomes (one per task per run). It never aborts the suite on a
// single task's failure: a task whose fork, setup, agent, getter, or metric
// errors scores 0 with Status [StatusError] and the error captured, and the run
// continues (AndroidWorld's try/except discipline, design 047 §9). The only
// errors Run itself returns are configuration errors (a backend that cannot
// satisfy the corpus's privilege tier, an unrunnable config) and a context
// cancellation, which stops the run cleanly with the outcomes gathered so far.
//
// Outcomes already recorded in cfg.Checkpoint are loaded and skipped, so a
// re-run resumes rather than repeats. The returned slice merges resumed and
// freshly scored outcomes in (task, run) order.
func Run(ctx context.Context, b Backend, cfg RunConfig) ([]Outcome, error) {
	if b == nil {
		return nil, fmt.Errorf("guibench run: backend is nil")
	}
	if cfg.Provider == "" {
		return nil, fmt.Errorf("guibench run: provider is empty")
	}
	if err := CanRun(b, cfg.Tasks); err != nil {
		return nil, fmt.Errorf("guibench run: %w", err)
	}
	runs := cfg.Runs
	if runs <= 0 {
		runs = 1
	}

	done := map[cell]Outcome{}
	if cfg.Checkpoint != nil {
		for _, o := range cfg.Checkpoint.Outcomes() {
			if o.Provider == cfg.Provider {
				done[cell{o.TaskID, o.Run}] = o
			}
		}
	}

	var outcomes []Outcome
	for _, t := range cfg.Tasks {
		for run := 0; run < runs; run++ {
			if err := ctx.Err(); err != nil {
				return outcomes, err
			}
			if prev, ok := done[cell{t.ID, run}]; ok {
				outcomes = append(outcomes, prev)
				if cfg.Observer != nil {
					cfg.Observer(prev)
				}
				continue
			}
			o := runOne(ctx, b, cfg, t, run)
			outcomes = append(outcomes, o)
			if cfg.Observer != nil {
				cfg.Observer(o)
			}
			if cfg.Checkpoint != nil {
				if err := cfg.Checkpoint.Append(o); err != nil {
					return outcomes, fmt.Errorf("guibench run: checkpoint: %w", err)
				}
			}
		}
	}
	sortOutcomes(outcomes)
	return outcomes, nil
}

// cell keys a (task, run) attempt for checkpoint dedup.
type cell struct {
	taskID string
	run    int
}

// runOne scores a single (task, run) attempt. It always returns an [Outcome]:
// any failure along the fork → setup → agent → flush → getter → metric path is
// captured into a Status [StatusError], Score 0 outcome, so the caller's suite
// loop never aborts (design 047 §9).
func runOne(ctx context.Context, b Backend, cfg RunConfig, t *Task, run int) Outcome {
	o := Outcome{
		Provider: cfg.Provider,
		Model:    cfg.Model,
		TaskID:   t.ID,
		Domain:   t.Domain,
		Run:      run,
		Status:   StatusScored,
	}
	score, err := scoreTask(ctx, b, cfg, t)
	if err != nil {
		o.Status = StatusError
		o.Score = 0
		o.Error = err.Error()
		return o
	}
	o.Score = score
	return o
}

// scoreTask runs one task against one fresh fork and returns its [0,1] score.
// It is the body of [runOne], separated so the error path is a single wrap.
func scoreTask(ctx context.Context, b Backend, cfg RunConfig, t *Task) (float64, error) {
	image := t.Image
	if cfg.Image != "" {
		image = cfg.Image
	}
	params := t.Params(cfg.ParamSeed)

	sess, err := b.Acquire(ctx, image)
	if err != nil {
		return 0, fmt.Errorf("acquire fork: %w", err)
	}
	defer sess.Close()
	probe := sess.Probe()

	if err := runSetup(probe, t, params); err != nil {
		return 0, fmt.Errorf("setup: %w", err)
	}

	answer, err := sess.RunAgent(ctx, Materialize(t.Instruction, params), StepBudget(t.Complexity))
	if err != nil {
		return 0, fmt.Errorf("agent: %w", err)
	}

	if err := runFlushes(probe, t); err != nil {
		return 0, fmt.Errorf("postconfig flush: %w", err)
	}

	score, err := Evaluate(probe, t, params, answer)
	if err != nil {
		return 0, fmt.Errorf("evaluate: %w", err)
	}
	return score, nil
}

// runSetup runs the task's ordered config steps against the fresh fork before
// the agent acts. Each step is an exec in the guest; a nonzero exit fails the
// task (a setup that cannot establish the precondition makes the score
// meaningless, design 047 §4).
func runSetup(p Probe, t *Task, params map[string]string) error {
	for i, step := range t.Config {
		args := materializeArgs(step.Args, params)
		if len(args) == 0 {
			return fmt.Errorf("step %d: empty args", i)
		}
		exit, _, stderr, err := p.Exec(args, step.Env, step.WorkDir)
		if err != nil {
			return fmt.Errorf("step %d (%v): %w", i, args, err)
		}
		if exit != 0 {
			return fmt.Errorf("step %d (%v) exited %d: %s", i, args, exit, stderr)
		}
	}
	return nil
}

// runFlushes runs the pre-read flushes a task's getters need before end-state
// is read (design 047 §7 postconfig hook). It always flushes cfprefsd so a
// defaults/plist read sees settled state, and checkpoints the WAL of any SQLite
// db a getter reads, so the verifier never reads stale async state — the most
// likely source of false negatives.
func runFlushes(p Probe, t *Task) error {
	if err := Flush(p, FlushCfprefsd, ""); err != nil {
		return err
	}
	for _, path := range sqlitePaths(t) {
		if err := Flush(p, FlushWAL, path); err != nil {
			return err
		}
	}
	return nil
}

// sqlitePaths returns the distinct, sorted db paths the task's getters read via
// the sqlite kind, so the runner checkpoints each WAL before scoring. tccdb and
// the sqlite getter already checkpoint inline; this covers any db a task reads a
// different way and keeps the flush explicit in the run record.
func sqlitePaths(t *Task) []string {
	seen := map[string]bool{}
	add := func(g GetterSpec) {
		if g.Kind == "sqlite" && g.Path != "" {
			seen[g.Path] = true
		}
	}
	add(t.Evaluator.Result)
	if t.Evaluator.Expected != nil {
		add(*t.Evaluator.Expected)
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// sortOutcomes orders outcomes by provider, then task id, then run, so a run's
// output is deterministic regardless of map iteration during resume.
func sortOutcomes(outcomes []Outcome) {
	sort.SliceStable(outcomes, func(i, j int) bool {
		a, b := outcomes[i], outcomes[j]
		if a.Provider != b.Provider {
			return a.Provider < b.Provider
		}
		if a.TaskID != b.TaskID {
			return a.TaskID < b.TaskID
		}
		return a.Run < b.Run
	})
}
