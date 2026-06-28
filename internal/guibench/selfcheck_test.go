package guibench

import (
	"strings"
	"testing"
)

// memGuest is a stateful fake guest for the self-check logic. The solution step
// "solve" flips solved to true; the getter command "probe" reports it as
// "true"/"false". This lets a unit test exercise the full self-check
// orchestration — fork, setup, solution, evaluate — without a VM, with a Probe
// whose state actually responds to the steps the self-check runs.
type memGuest struct {
	solved bool
}

func (g *memGuest) Exec(args []string, _ map[string]string, _ string) (int, string, string, error) {
	switch strings.Join(args, " ") {
	case "solve":
		g.solved = true
		return 0, "", "", nil
	case "reset":
		g.solved = false
		return 0, "", "", nil
	case "probe":
		if g.solved {
			return 0, "true\n", "", nil
		}
		return 0, "false\n", "", nil
	default:
		return 0, "", "", nil // unrecognized setup steps are no-ops
	}
}

func (g *memGuest) ReadFile(string) ([]byte, error) { return nil, nil }
func (g *memGuest) OCRAllText() (string, error)     { return "", nil }
func (g *memGuest) Probe() Probe                    { return g }
func (g *memGuest) Close() error                    { return nil }

// memEnv hands out a fresh memGuest per Acquire, so the no-op run never sees the
// good run's solved state (design 047 §6: one fresh fork per run).
type memEnv struct{ acquired int }

func (e *memEnv) Acquire(string) (SelfCheckSession, error) {
	e.acquired++
	return &memGuest{}, nil
}

func feasibleTask() *Task {
	return &Task{
		ID:    "mem-feasible",
		Image: "macos-base:v1",
		Config: []SetupStep{
			{Args: []string{"reset"}},
		},
		Solution: []SetupStep{
			{Args: []string{"solve"}},
		},
		Evaluator: Evaluator{
			Func:   StringList{"file_exists"},
			Result: GetterSpec{Kind: "exec", Args: []string{"probe"}},
		},
	}
}

func TestSelfCheckFeasible(t *testing.T) {
	env := &memEnv{}
	r := SelfCheck(env, feasibleTask(), 1)
	if r.Err != nil {
		t.Fatalf("SelfCheck error: %v", r.Err)
	}
	if r.Good != 1 {
		t.Fatalf("good score = %v, want 1 (solution must solve the task)", r.Good)
	}
	if r.NoOp != 0 {
		t.Fatalf("no-op score = %v, want 0 (no-op must not solve the task)", r.NoOp)
	}
	if !r.OK {
		t.Fatalf("OK = false, want true; result: %s", r)
	}
	// Good run and no-op run must each acquire their own fresh environment.
	if env.acquired != 2 {
		t.Fatalf("acquired %d environments, want 2 (fresh fork per run)", env.acquired)
	}
}

func TestSelfCheckInfeasible(t *testing.T) {
	// An infeasible task succeeds only when the agent declines (answers FAIL).
	// The good run answers FAIL (score 1); the no-op answers nothing (score 0).
	task := &Task{
		ID:         "mem-infeasible",
		Image:      "macos-base:v1",
		Infeasible: true,
		Evaluator: Evaluator{
			Func:   StringList{"infeasible"},
			Result: GetterSpec{Kind: "exec", Args: []string{"true"}},
		},
	}
	r := SelfCheck(&memEnv{}, task, 1)
	if r.Err != nil {
		t.Fatalf("SelfCheck error: %v", r.Err)
	}
	if r.Good != 1 || r.NoOp != 0 || !r.OK {
		t.Fatalf("infeasible self-check = %s, want good=1 noop=0 OK", r)
	}
}

func TestSelfCheckDetectsBrokenVerifier(t *testing.T) {
	// A verifier that scores the no-op as 1 (e.g. checks a condition the setup
	// already satisfies) is miscalibrated; the self-check must catch it. Here the
	// getter ignores solved state and always reports present.
	task := feasibleTask()
	task.ID = "mem-broken"
	task.Evaluator.Result = GetterSpec{Kind: "exec", Args: []string{"always-true"}}
	// Use an env whose getter always returns true regardless of the solution.
	env := alwaysTrueEnv{}
	r := SelfCheck(env, task, 1)
	if r.Err != nil {
		t.Fatalf("SelfCheck error: %v", r.Err)
	}
	if r.OK {
		t.Fatalf("OK = true, want false; a verifier that passes the no-op is broken: %s", r)
	}
	if r.NoOp != 1 {
		t.Fatalf("no-op score = %v, want 1 (the deliberately broken verifier)", r.NoOp)
	}
}

// alwaysTrueProbe reports any "always-true" exec as present, ignoring state.
type alwaysTrueProbe struct{}

func (alwaysTrueProbe) Exec(args []string, _ map[string]string, _ string) (int, string, string, error) {
	if strings.Join(args, " ") == "always-true" {
		return 0, "true\n", "", nil
	}
	return 0, "", "", nil
}
func (alwaysTrueProbe) ReadFile(string) ([]byte, error) { return nil, nil }
func (alwaysTrueProbe) OCRAllText() (string, error)     { return "", nil }
func (alwaysTrueProbe) Probe() Probe                    { return alwaysTrueProbe{} }
func (alwaysTrueProbe) Close() error                    { return nil }

type alwaysTrueEnv struct{}

func (alwaysTrueEnv) Acquire(string) (SelfCheckSession, error) { return alwaysTrueProbe{}, nil }

func TestSelfCheckRejectsNoSolution(t *testing.T) {
	// A feasible task with no solution cannot be self-checked.
	task := feasibleTask()
	task.ID = "mem-no-solution"
	task.Solution = nil
	r := SelfCheck(&memEnv{}, task, 1)
	if r.Err == nil {
		t.Fatalf("want error for feasible task with no solution, got %s", r)
	}
	if !strings.Contains(r.Err.Error(), "no solution") {
		t.Fatalf("error = %v, want it to mention the missing solution", r.Err)
	}
}

func TestSelfCheckCorpus(t *testing.T) {
	tasks := []*Task{feasibleTask(), feasibleTask()}
	tasks[1].ID = "mem-feasible-2"
	results, err := SelfCheckCorpus(&memEnv{}, tasks, 1)
	if err != nil {
		t.Fatalf("SelfCheckCorpus: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}

	// A corpus containing a broken-verifier task fails the gate.
	broken := feasibleTask()
	broken.ID = "mem-broken"
	broken.Evaluator.Result = GetterSpec{Kind: "exec", Args: []string{"always-true"}}
	_, err = SelfCheckCorpus(alwaysTrueEnv{}, []*Task{broken}, 1)
	if err == nil {
		t.Fatalf("SelfCheckCorpus accepted a corpus with a broken verifier")
	}
}

func TestSummarizeCalibration(t *testing.T) {
	results := []SelfCheckResult{
		{TaskID: "a", Seed: 7, Good: 1, NoOp: 0, OK: true},
		{TaskID: "b", Seed: 7, Good: 1, NoOp: 1, OK: false}, // miscalibrated: no-op also scores 1
		{TaskID: "c", Seed: 7, Err: errNoSolution},          // could not self-check
		{TaskID: "a", Seed: 7, Good: 1, NoOp: 0, OK: true},  // duplicate id counted once
	}
	s := SummarizeCalibration(results)
	if s.Tasks != 3 {
		t.Errorf("Tasks = %d, want 3 (duplicate id collapsed)", s.Tasks)
	}
	if s.Verified != 1 || s.Failed != 1 || s.Errored != 1 {
		t.Errorf("Verified/Failed/Errored = %d/%d/%d, want 1/1/1", s.Verified, s.Failed, s.Errored)
	}
	if s.Verified+s.Failed+s.Errored != s.Tasks {
		t.Errorf("buckets %d+%d+%d do not sum to Tasks %d", s.Verified, s.Failed, s.Errored, s.Tasks)
	}
	if s.Seed != 7 {
		t.Errorf("Seed = %d, want 7", s.Seed)
	}
	if got := s.Headline(); !strings.Contains(got, "1/3 verifier-calibrated") ||
		!strings.Contains(got, "1 miscalibrated") || !strings.Contains(got, "1 errored") {
		t.Errorf("Headline = %q, want the verified/miscalibrated/errored breakdown", got)
	}
}

func TestCalibrationHeadlineAllVerified(t *testing.T) {
	s := SummarizeCalibration([]SelfCheckResult{
		{TaskID: "a", Seed: 3, OK: true},
		{TaskID: "b", Seed: 3, OK: true},
	})
	got := s.Headline()
	if !strings.Contains(got, "2 tasks: all verifier-calibrated") {
		t.Errorf("all-verified Headline = %q, want clean N-tasks claim", got)
	}
	if strings.Contains(got, "miscalibrated") || strings.Contains(got, "errored") {
		t.Errorf("all-verified Headline leaked a failure clause: %q", got)
	}
	if SummarizeCalibration(nil).Headline() != "no tasks self-checked" {
		t.Errorf("empty Headline = %q", SummarizeCalibration(nil).Headline())
	}
}
