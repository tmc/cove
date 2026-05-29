package guibench

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeSession is an in-memory [Session] for testing the runner without a VM.
// It serves getter reads from a [FakeProbe], returns a canned agent answer, and
// bumps its backend's close counter so a test can assert each fork is discarded.
type fakeSession struct {
	backend  *fakeBackend
	answer   string
	agentErr error
}

func (s *fakeSession) Probe() Probe { return s.backend.probe }

func (s *fakeSession) RunAgent(_ context.Context, _ string, _ int) (string, error) {
	if s.agentErr != nil {
		return "", s.agentErr
	}
	return s.answer, nil
}

func (s *fakeSession) Close() error {
	s.backend.closed++
	return nil
}

// fakeBackend hands out a fresh fakeSession per Acquire, so each task sees an
// independent environment — the in-memory analogue of one fresh fork per task.
// acquireErr forces an Acquire failure (the fork-crash path); tier caps what
// the backend claims to satisfy.
type fakeBackend struct {
	tier       Tier
	answer     string
	agentErr   error
	acquireErr error
	probe      Probe
	acquired   int
	closed     int
}

func (b *fakeBackend) Acquire(_ context.Context, _ string) (Session, error) {
	b.acquired++
	if b.acquireErr != nil {
		return nil, b.acquireErr
	}
	if b.probe == nil {
		b.probe = FakeProbe{}
	}
	return &fakeSession{backend: b, answer: b.answer, agentErr: b.agentErr}, nil
}

func (b *fakeBackend) MaxTier() Tier { return b.tier }

func TestRunScoresTask(t *testing.T) {
	// A folder-exists task: the getter reads exit 0, the metric matches "0".
	task := &Task{
		ID:     "make-folder",
		Domain: "Finder",
		Image:  "macos-base:v1",
		Evaluator: Evaluator{
			Func:    StringList{"exact_match"},
			Result:  GetterSpec{Kind: "exec", Args: []string{"test", "-d", "/Users/tmc/Desktop/Project"}, Field: "exit"},
			Options: map[string]any{"expected": "0"},
		},
	}
	probe := FakeProbe{Commands: map[string]ExecResult{
		"test -d /Users/tmc/Desktop/Project": {ExitCode: 0},
		"killall cfprefsd":                   {ExitCode: 0},
	}}
	b := &fakeBackend{tier: TierA, probe: probe}

	outcomes, err := Run(context.Background(), b, RunConfig{
		Tasks:    []*Task{task},
		Provider: "anthropic",
		Runs:     1,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(outcomes) != 1 {
		t.Fatalf("got %d outcomes, want 1", len(outcomes))
	}
	o := outcomes[0]
	if o.Status != StatusScored || o.Score != 1 {
		t.Fatalf("outcome = %+v, want scored score=1", o)
	}
	if o.Provider != "anthropic" || o.TaskID != "make-folder" || o.Domain != "Finder" {
		t.Fatalf("outcome identity = %+v", o)
	}
	if b.acquired != 1 {
		t.Fatalf("acquired %d times, want 1 (one fresh fork per task)", b.acquired)
	}
}

func TestRunCrashScoresZeroAndContinues(t *testing.T) {
	// Two tasks: the first task's agent crashes; the suite must continue and the
	// second task must still score. This is the AndroidWorld try/except contract.
	good := &Task{
		ID:    "good",
		Image: "img",
		Evaluator: Evaluator{
			Func:    StringList{"exact_match"},
			Result:  GetterSpec{Kind: "exec", Args: []string{"echo", "ok"}},
			Options: map[string]any{"expected": "ok"},
		},
	}
	bad := &Task{
		ID:    "bad",
		Image: "img",
		Evaluator: Evaluator{
			Func:   StringList{"exact_match"},
			Result: GetterSpec{Kind: "exec", Args: []string{"echo", "ok"}},
		},
	}
	probe := FakeProbe{Commands: map[string]ExecResult{
		"echo ok":          {ExitCode: 0, Stdout: "ok\n"},
		"killall cfprefsd": {ExitCode: 0},
	}}
	// A backend whose agent always errors makes every task crash.
	b := &fakeBackend{tier: TierA, probe: probe, agentErr: errors.New("provider timeout")}

	outcomes, err := Run(context.Background(), b, RunConfig{
		Tasks:    []*Task{bad, good},
		Provider: "openai",
		Runs:     1,
	})
	if err != nil {
		t.Fatalf("Run must not abort on a task crash: %v", err)
	}
	if len(outcomes) != 2 {
		t.Fatalf("got %d outcomes, want 2 (suite continued)", len(outcomes))
	}
	for _, o := range outcomes {
		if o.Status != StatusError || o.Score != 0 {
			t.Fatalf("crashed task %s: status=%s score=%v, want error/0", o.TaskID, o.Status, o.Score)
		}
		if o.Error == "" {
			t.Fatalf("crashed task %s: empty error, want captured cause", o.TaskID)
		}
	}
	if b.acquired != 2 {
		t.Fatalf("acquired %d, want 2 (a crash still consumes one fork per task)", b.acquired)
	}
}

func TestRunSetupFailureScoresZero(t *testing.T) {
	task := &Task{
		ID:    "needs-setup",
		Image: "img",
		Config: []SetupStep{
			{Args: []string{"mkdir", "/Users/tmc/Desktop/Seed"}},
		},
		Evaluator: Evaluator{
			Func:   StringList{"exact_match"},
			Result: GetterSpec{Kind: "exec", Args: []string{"echo", "x"}},
		},
	}
	// The setup mkdir exits nonzero: the task scores 0 with the setup error.
	probe := FakeProbe{Commands: map[string]ExecResult{
		"mkdir /Users/tmc/Desktop/Seed": {ExitCode: 1, Stderr: "permission denied"},
	}}
	b := &fakeBackend{tier: TierA, probe: probe}
	outcomes, err := Run(context.Background(), b, RunConfig{Tasks: []*Task{task}, Provider: "p", Runs: 1})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	o := outcomes[0]
	if o.Status != StatusError || o.Score != 0 {
		t.Fatalf("setup-failed task = %+v, want error/0", o)
	}
	if !strings.Contains(o.Error, "setup") {
		t.Fatalf("error %q does not mention %q", o.Error, "setup")
	}
}

func TestRunRejectsUndergrantedBackend(t *testing.T) {
	// A task whose getter needs Tier C on a Tier-A backend is a config error:
	// the runner refuses rather than reading denied state (design 047 §5, §12).
	task := &Task{
		ID:    "ax",
		Image: "img",
		Evaluator: Evaluator{
			Func:   StringList{"exact_match"},
			Result: GetterSpec{Kind: "accessibility", App: "Safari", Attr: "value"},
		},
	}
	b := &fakeBackend{tier: TierA}
	_, err := Run(context.Background(), b, RunConfig{Tasks: []*Task{task}, Provider: "p", Runs: 1})
	if err == nil {
		t.Fatalf("Run accepted a Tier-C corpus on a Tier-A backend")
	}
}

func TestRunMultipleRunsPerTask(t *testing.T) {
	task := &Task{
		ID:    "t",
		Image: "img",
		Evaluator: Evaluator{
			Func:    StringList{"exact_match"},
			Result:  GetterSpec{Kind: "exec", Args: []string{"echo", "ok"}},
			Options: map[string]any{"expected": "ok"},
		},
	}
	probe := FakeProbe{Commands: map[string]ExecResult{
		"echo ok":          {ExitCode: 0, Stdout: "ok\n"},
		"killall cfprefsd": {ExitCode: 0},
	}}
	b := &fakeBackend{tier: TierA, probe: probe}
	outcomes, err := Run(context.Background(), b, RunConfig{Tasks: []*Task{task}, Provider: "p", Runs: 3})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(outcomes) != 3 {
		t.Fatalf("got %d outcomes, want 3 (one per run)", len(outcomes))
	}
	for i, o := range outcomes {
		if o.Run != i {
			t.Fatalf("outcome %d has run index %d", i, o.Run)
		}
	}
	if b.acquired != 3 {
		t.Fatalf("acquired %d, want 3 (a fresh fork per run, never reused)", b.acquired)
	}
}

func TestRunContextCancelStops(t *testing.T) {
	task := &Task{
		ID:    "t",
		Image: "img",
		Evaluator: Evaluator{
			Func:   StringList{"exact_match"},
			Result: GetterSpec{Kind: "exec", Args: []string{"echo", "ok"}},
		},
	}
	b := &fakeBackend{tier: TierA, probe: FakeProbe{}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	outcomes, err := Run(ctx, b, RunConfig{Tasks: []*Task{task}, Provider: "p", Runs: 1})
	if err == nil {
		t.Fatalf("Run with cancelled context returned nil error")
	}
	if len(outcomes) != 0 {
		t.Fatalf("got %d outcomes from a cancelled run, want 0", len(outcomes))
	}
}

// Confirm the in-memory fakes satisfy the production interfaces at compile time.
var (
	_ Backend = (*fakeBackend)(nil)
	_ Session = (*fakeSession)(nil)
)
