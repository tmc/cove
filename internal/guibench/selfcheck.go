package guibench

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// SelfCheck verifies that a task's verifier recognizes its own gold solution
// and rejects a no-op (design 047 §9 slice 4). It is the AndroidWorld "is the
// validator correct" discipline applied to a single task: a verifier that
// cannot tell a solved guest from an untouched one is broken, and the task is
// unusable regardless of how any agent does on it.
//
// SelfCheck runs the task twice through env:
//
//   - Good run: setup (Config) then the known-good Solution, then Evaluate.
//     A correct verifier scores 1.
//   - No-op run: setup (Config) only — no Solution — then Evaluate. A correct
//     verifier scores 0, because nothing solved the task.
//
// The two runs use independent environments (a fresh fork per run, design 047
// §6), so the no-op run never sees the good run's state. SelfCheck is pure
// orchestration: every macOS interaction goes through [SelfCheckEnv], so the
// logic is unit-testable against a fake env without a VM.
func SelfCheck(env SelfCheckEnv, t *Task, seed uint64) SelfCheckResult {
	res := SelfCheckResult{TaskID: t.ID, Seed: seed}
	if err := t.CheckSelfCheckable(); err != nil {
		res.Err = err
		return res
	}
	params := t.Params(seed)

	good, err := scoreRun(env, t, params, true)
	if err != nil {
		res.Err = fmt.Errorf("good run: %w", err)
		return res
	}
	res.Good = good

	noop, err := scoreRun(env, t, params, false)
	if err != nil {
		res.Err = fmt.Errorf("no-op run: %w", err)
		return res
	}
	res.NoOp = noop

	res.OK = good == 1 && noop == 0
	return res
}

// scoreRun acquires a fresh environment, runs the task's setup, optionally runs
// the known-good solution, then evaluates. It always closes the environment.
func scoreRun(env SelfCheckEnv, t *Task, params map[string]string, solve bool) (score float64, err error) {
	sess, err := env.Acquire(t.Image)
	if err != nil {
		return 0, fmt.Errorf("acquire: %w", err)
	}
	defer func() {
		if cerr := sess.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close: %w", cerr)
		}
	}()

	if err := runSteps(sess.Probe(), t.Config, params); err != nil {
		return 0, fmt.Errorf("setup: %w", err)
	}
	if solve {
		if err := runSteps(sess.Probe(), t.Solution, params); err != nil {
			return 0, fmt.Errorf("solution: %w", err)
		}
	}

	// An infeasible task has no on-guest end-state: success is the agent's
	// terminal answer. The good run answers FAIL (the gold solution to an
	// impossible task); the no-op answers nothing.
	answer := ""
	if t.Infeasible {
		answer = t.SolutionAnswer(solve)
	}
	score, err = Evaluate(sess.Probe(), t, params, answer)
	if err != nil {
		return 0, err
	}
	return score, nil
}

// runSteps executes ordered setup or solution steps against the probe,
// materializing {PARAM} placeholders in each argv. A nonzero exit is not an
// error here (a step may legitimately fail, e.g. deleting an absent file); only
// a transport error stops the run.
func runSteps(p Probe, steps []SetupStep, params map[string]string) error {
	for i, s := range steps {
		args := materializeArgs(s.Args, params)
		if len(args) == 0 {
			return fmt.Errorf("step %d: empty args", i)
		}
		if _, _, _, err := p.Exec(args, s.Env, s.WorkDir); err != nil {
			return fmt.Errorf("step %d (%v): %w", i, args, err)
		}
	}
	return nil
}

// SolutionAnswer returns the terminal answer the gold solution gives for an
// infeasible task: "FAIL" on the good run (the agent correctly declines) and ""
// on the no-op run (the agent did not decline). For a feasible task the answer
// is unused, so it is always empty.
func (t *Task) SolutionAnswer(solve bool) string {
	if t.Infeasible && solve {
		return "FAIL"
	}
	return ""
}

// SelfCheckEnv provisions the hermetic environments a [SelfCheck] runs in. The
// live implementation forks an ephemeral cove VM per call (design 047 §6); the
// unit test supplies a fake that returns canned [Probe] state, so the
// self-check logic is verified without a VM.
type SelfCheckEnv interface {
	// Acquire provisions a fresh environment to fork-from image and returns a
	// session bound to it. Each call must yield an environment that has seen no
	// prior run's state.
	Acquire(image string) (SelfCheckSession, error)
}

// SelfCheckSession is one self-check run's hold on a hermetic environment: a
// [Probe] to run steps and read end-state, and a Close to discard it.
type SelfCheckSession interface {
	Probe() Probe
	Close() error
}

// BackendEnv adapts a [Backend] (the substrate the scored runner forks on) to a
// [SelfCheckEnv], so the live self-check and the live scored run share one fork
// path (the reference vzForkBackend). The self-check ignores the Backend's
// agent loop — it runs the gold solution, not a model — so only Acquire/Probe/
// Close are used. ctx bounds every fork's provisioning.
func BackendEnv(ctx context.Context, b Backend) SelfCheckEnv {
	return backendEnv{ctx: ctx, b: b}
}

type backendEnv struct {
	ctx context.Context
	b   Backend
}

func (e backendEnv) Acquire(image string) (SelfCheckSession, error) {
	sess, err := e.b.Acquire(e.ctx, image)
	if err != nil {
		return nil, err
	}
	return backendSession{sess}, nil
}

// backendSession exposes only the self-check half of a [Session] (Probe and
// Close), dropping RunAgent.
type backendSession struct{ s Session }

func (b backendSession) Probe() Probe { return b.s.Probe() }
func (b backendSession) Close() error { return b.s.Close() }

// SelfCheckResult is the outcome of [SelfCheck] for one task. OK is true only
// when the verifier scored the gold solution 1 and the no-op 0; any other
// combination means the verifier is miscalibrated for that task.
type SelfCheckResult struct {
	TaskID string
	Seed   uint64
	Good   float64 // score of the known-good solution; want 1
	NoOp   float64 // score of the no-op run; want 0
	OK     bool
	Err    error
}

// String formats the result as a one-line report line.
func (r SelfCheckResult) String() string {
	status := "FAIL"
	if r.OK {
		status = "OK"
	}
	if r.Err != nil {
		return fmt.Sprintf("%-4s %-28s good=?    noop=?    error: %v", "ERR", r.TaskID, r.Err)
	}
	return fmt.Sprintf("%-4s %-28s good=%.0f noop=%.0f", status, r.TaskID, r.Good, r.NoOp)
}

// SelfCheckCorpus runs [SelfCheck] for every task and reports the aggregate. It
// is the engine behind `cove bench gui selfcheck`. tasks must already be
// validated (e.g. via [Load]). The returned error is non-nil iff any task's
// verifier is miscalibrated or errored, so a caller can fail a CI gate on it.
func SelfCheckCorpus(env SelfCheckEnv, tasks []*Task, seed uint64) ([]SelfCheckResult, error) {
	results := make([]SelfCheckResult, 0, len(tasks))
	var failures int
	for _, t := range tasks {
		r := SelfCheck(env, t, seed)
		results = append(results, r)
		if !r.OK {
			failures++
		}
	}
	if failures > 0 {
		return results, fmt.Errorf("%d of %d task(s) failed the verifier self-check", failures, len(tasks))
	}
	return results, nil
}

// CalibrationSummary is the corpus-level verifier-calibration rollup published
// in the citable report, the AndroidWorld "is the validator correct" discipline
// applied corpus-wide: how many of the scored tasks have a verifier that scores
// its own gold solution 1 and a no-op 0 (design 047 §9 slice 4). A task whose
// verifier cannot tell a solved guest from an untouched one is miscalibrated and
// its agent scores are meaningless; the summary makes that count visible next to
// the rigor headline rather than leaving a reader to assume it. It is derived
// purely from the per-task [SelfCheckResult]s, mirroring [RigorSummary].
type CalibrationSummary struct {
	// Tasks is the number of distinct tasks the summary covers.
	Tasks int `json:"tasks"`
	// Verified is how many tasks passed the self-check (gold solution scored 1,
	// no-op scored 0). Failed is how many ran but were miscalibrated; Errored is
	// how many could not complete the self-check (e.g. a fork or setup failure).
	// They sum to Tasks.
	Verified int `json:"verified"`
	Failed   int `json:"failed"`
	Errored  int `json:"errored"`
	// Seed is the parameter seed the self-check ran under, recorded so the claim
	// is reproducible.
	Seed uint64 `json:"seed"`
}

// SummarizeCalibration rolls a set of per-task [SelfCheckResult]s into a
// corpus-level [CalibrationSummary]. Duplicate task ids are counted once (the
// last result for an id wins), so passing repeated results is safe. The seed is
// taken from the results; mixed seeds keep the largest, which the caller should
// avoid by self-checking the whole corpus under one seed.
func SummarizeCalibration(results []SelfCheckResult) CalibrationSummary {
	byTask := make(map[string]SelfCheckResult, len(results))
	for _, r := range results {
		byTask[r.TaskID] = r
	}
	var s CalibrationSummary
	for _, r := range byTask {
		s.Tasks++
		if r.Seed > s.Seed {
			s.Seed = r.Seed
		}
		switch {
		case r.Err != nil:
			s.Errored++
		case r.OK:
			s.Verified++
		default:
			s.Failed++
		}
	}
	return s
}

// Headline renders the calibration summary as a one-line citable claim:
// "N tasks verifier-calibrated" when all pass, otherwise the verified/failed/
// errored breakdown so a reader sees exactly how trustworthy the corpus is.
func (s CalibrationSummary) Headline() string {
	if s.Tasks == 0 {
		return "no tasks self-checked"
	}
	if s.Verified == s.Tasks {
		return pluralTasks(s.Tasks, "tasks") +
			": all verifier-calibrated (gold solution scores 1, no-op scores 0)"
	}
	parts := []string{fmt.Sprintf("%d/%d verifier-calibrated", s.Verified, s.Tasks)}
	if s.Failed > 0 {
		parts = append(parts, fmt.Sprintf("%d miscalibrated", s.Failed))
	}
	if s.Errored > 0 {
		parts = append(parts, fmt.Sprintf("%d errored", s.Errored))
	}
	return strings.Join(parts, ", ")
}

// errNoSolution reports a task that has no gold solution yet cannot succeed
// from a no-op (i.e. not infeasible). Such a task cannot be self-checked.
var errNoSolution = errors.New("task has no solution and is not infeasible; cannot self-check")

// CheckSelfCheckable reports whether the task can be self-checked at all: a
// feasible task needs a non-empty Solution (the gold steps), while an
// infeasible task is self-checked through its terminal answer and needs none.
func (t *Task) CheckSelfCheckable() error {
	if t.Infeasible {
		return nil
	}
	if len(t.Solution) == 0 {
		return fmt.Errorf("task %s: %w", t.ID, errNoSolution)
	}
	return nil
}
