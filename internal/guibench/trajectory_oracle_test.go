package guibench

import "testing"

// recordingGuest is a stateful fake guest for oracle-trajectory tests: the
// solution step "solve" marks it solved, the getter command "probe" reports the
// state, OCR returns a fixed observation, and (when shots is true) it satisfies
// [Screenshotter] so the recorder captures synthetic pixels.
type recordingGuest struct {
	solved bool
	shots  bool
}

func (g *recordingGuest) Exec(args []string, _ map[string]string, _ string) (int, string, string, error) {
	if len(args) == 1 && args[0] == "solve" {
		g.solved = true
	}
	if len(args) == 1 && args[0] == "probe" {
		if g.solved {
			return 0, "yes", "", nil
		}
		return 0, "no", "", nil
	}
	return 0, "", "", nil
}
func (g *recordingGuest) ReadFile(string) ([]byte, error) { return nil, nil }
func (g *recordingGuest) OCRAllText() (string, error)     { return "screen text", nil }
func (g *recordingGuest) Screenshot() ([]byte, error) {
	if !g.shots {
		return nil, errNoSolution // any error -> recorder degrades to text-only
	}
	return []byte("\x89PNG\r\n\x1a\nfake"), nil
}
func (g *recordingGuest) Probe() Probe { return g }
func (g *recordingGuest) Close() error { return nil }

type recordingEnv struct{ shots bool }

func (e recordingEnv) Acquire(string) (SelfCheckSession, error) {
	return &recordingGuest{shots: e.shots}, nil
}

func oracleTask() *Task {
	return &Task{
		ID:          "demo-oracle",
		Image:       "macos-base:v1",
		Domain:      "Finder",
		Instruction: "Solve the demo",
		Config:      []SetupStep{{Args: []string{"reset"}}},
		Solution:    []SetupStep{{Args: []string{"solve"}}, {Args: []string{"confirm"}}},
		Evaluator: Evaluator{
			Func:    StringList{"exact_match"},
			Result:  GetterSpec{Kind: "exec", Args: []string{"probe"}},
			Options: map[string]any{"expected": "yes"},
		},
	}
}

func TestRecordOracleTrajectoryWithScreenshots(t *testing.T) {
	traj, shots, err := RecordOracleTrajectory(recordingEnv{shots: true}, oracleTask(), 1)
	if err != nil {
		t.Fatalf("RecordOracleTrajectory: %v", err)
	}
	if traj.Source != SourceOracle || traj.Provider != SourceOracle {
		t.Errorf("source/provider = %q/%q want oracle", traj.Source, traj.Provider)
	}
	if traj.Reward != 1 {
		t.Errorf("oracle reward = %v want 1 (gold solution must score 1)", traj.Reward)
	}
	if len(traj.Steps) != 2 {
		t.Fatalf("steps = %d want 2 (one per solution action)", len(traj.Steps))
	}
	if traj.Steps[0].Action != "solve" || traj.Steps[1].Action != "confirm" {
		t.Errorf("step actions = %q,%q", traj.Steps[0].Action, traj.Steps[1].Action)
	}
	for i, s := range traj.Steps {
		if s.Observation != "screen text" {
			t.Errorf("step %d observation = %q", i, s.Observation)
		}
		if s.Screenshot == "" {
			t.Errorf("step %d has no screenshot but env is a screenshotter", i)
		}
		if _, ok := shots[s.Screenshot]; !ok {
			t.Errorf("step %d screenshot %q not in shots map", i, s.Screenshot)
		}
	}
	if len(shots) != 2 {
		t.Errorf("shots = %d want 2", len(shots))
	}
}

func TestRecordOracleTrajectoryTextOnly(t *testing.T) {
	traj, shots, err := RecordOracleTrajectory(recordingEnv{shots: false}, oracleTask(), 1)
	if err != nil {
		t.Fatalf("RecordOracleTrajectory: %v", err)
	}
	if len(shots) != 0 {
		t.Errorf("shots = %d want 0 (no screenshotter)", len(shots))
	}
	for i, s := range traj.Steps {
		if s.Screenshot != "" {
			t.Errorf("step %d screenshot = %q want empty", i, s.Screenshot)
		}
		if s.Observation != "screen text" {
			t.Errorf("step %d observation = %q want OCR text", i, s.Observation)
		}
	}
}

func TestRecordOracleTrajectoryInfeasible(t *testing.T) {
	task := &Task{
		ID:          "demo-infeasible",
		Image:       "macos-base:v1",
		Instruction: "Do the impossible",
		Infeasible:  true,
		Evaluator: Evaluator{
			Func:   StringList{"infeasible"},
			Result: GetterSpec{Kind: "exec", Args: []string{"probe"}},
		},
	}
	traj, _, err := RecordOracleTrajectory(recordingEnv{}, task, 1)
	if err != nil {
		t.Fatalf("RecordOracleTrajectory: %v", err)
	}
	if len(traj.Steps) != 1 {
		t.Fatalf("infeasible steps = %d want 1 terminal step", len(traj.Steps))
	}
	if traj.Answer != "FAIL" {
		t.Errorf("infeasible answer = %q want FAIL", traj.Answer)
	}
	if traj.Reward != 1 {
		t.Errorf("infeasible reward = %v want 1", traj.Reward)
	}
}

func TestRecordOracleCorpusSkipsUnsolvable(t *testing.T) {
	solvable := oracleTask()
	unsolvable := &Task{ // no solution, not infeasible -> not self-checkable -> skipped
		ID:          "no-solution",
		Image:       "macos-base:v1",
		Instruction: "No gold steps",
		Evaluator:   Evaluator{Func: StringList{"file_exists"}, Result: GetterSpec{Kind: "exec", Args: []string{"probe"}}},
	}
	trajs, _, err := RecordOracleCorpus(recordingEnv{}, []*Task{solvable, unsolvable}, 1)
	if err != nil {
		t.Fatalf("RecordOracleCorpus: %v", err)
	}
	if len(trajs) != 1 {
		t.Fatalf("trajectories = %d want 1 (unsolvable skipped)", len(trajs))
	}
	if trajs[0].TaskID != "demo-oracle" {
		t.Errorf("recorded task = %q want demo-oracle", trajs[0].TaskID)
	}
}
