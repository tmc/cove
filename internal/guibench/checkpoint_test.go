package guibench

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckpointAppendAndReload(t *testing.T) {
	dir := t.TempDir()
	cp, err := OpenCheckpoint(dir)
	if err != nil {
		t.Fatalf("OpenCheckpoint: %v", err)
	}
	want := []Outcome{
		{Provider: "anthropic", TaskID: "a", Run: 0, Score: 1, Status: StatusScored},
		{Provider: "anthropic", TaskID: "b", Run: 0, Score: 0, Status: StatusError, Error: "boom"},
	}
	for _, o := range want {
		if err := cp.Append(o); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	if err := cp.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen: the prior outcomes must be loaded for resume.
	cp2, err := OpenCheckpoint(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer cp2.Close()
	got := cp2.Outcomes()
	if len(got) != len(want) {
		t.Fatalf("reloaded %d outcomes, want %d", len(got), len(want))
	}
	if !cp2.Has("anthropic", "a", 0) || !cp2.Has("anthropic", "b", 0) {
		t.Fatalf("Has missed a recorded cell")
	}
	if cp2.Has("anthropic", "c", 0) {
		t.Fatalf("Has reported an unrecorded cell")
	}
	if got[1].Error != "boom" {
		t.Fatalf("reloaded error = %q, want boom", got[1].Error)
	}
}

func TestCheckpointMalformedLineIsHardError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, CheckpointFile)
	if err := os.WriteFile(path, []byte("{not json\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := OpenCheckpoint(dir); err == nil {
		t.Fatalf("OpenCheckpoint accepted a malformed log")
	}
}

func TestRunResumesFromCheckpoint(t *testing.T) {
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
	dir := t.TempDir()

	// Pre-seed the checkpoint as if run 0 already completed.
	seed, err := OpenCheckpoint(dir)
	if err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}
	if err := seed.Append(Outcome{Provider: "p", TaskID: "t", Run: 0, Score: 1, Status: StatusScored}); err != nil {
		t.Fatalf("seed append: %v", err)
	}
	seed.Close()

	cp, err := OpenCheckpoint(dir)
	if err != nil {
		t.Fatalf("reopen checkpoint: %v", err)
	}
	defer cp.Close()

	b := &fakeBackend{tier: TierA, probe: probe}
	outcomes, err := Run(context.Background(), b, RunConfig{
		Tasks:      []*Task{task},
		Provider:   "p",
		Runs:       2,
		Checkpoint: cp,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(outcomes) != 2 {
		t.Fatalf("got %d outcomes, want 2", len(outcomes))
	}
	// Run 0 came from the checkpoint (no fork acquired); only run 1 was scored
	// fresh, so the backend acquired exactly one fork.
	if b.acquired != 1 {
		t.Fatalf("acquired %d forks, want 1 (run 0 resumed from checkpoint)", b.acquired)
	}
	for _, o := range outcomes {
		if o.Score != 1 || o.Status != StatusScored {
			t.Fatalf("outcome = %+v, want scored/1", o)
		}
	}
}

func TestRunDiscardsEveryFork(t *testing.T) {
	tasks := []*Task{
		{ID: "a", Image: "img", Evaluator: Evaluator{Func: StringList{"exact_match"}, Result: GetterSpec{Kind: "exec", Args: []string{"echo", "ok"}}, Options: map[string]any{"expected": "ok"}}},
		{ID: "b", Image: "img", Evaluator: Evaluator{Func: StringList{"exact_match"}, Result: GetterSpec{Kind: "exec", Args: []string{"echo", "ok"}}, Options: map[string]any{"expected": "ok"}}},
	}
	probe := FakeProbe{Commands: map[string]ExecResult{
		"echo ok":          {ExitCode: 0, Stdout: "ok\n"},
		"killall cfprefsd": {ExitCode: 0},
	}}
	b := &fakeBackend{tier: TierA, probe: probe}
	if _, err := Run(context.Background(), b, RunConfig{Tasks: tasks, Provider: "p", Runs: 2}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// 2 tasks * 2 runs = 4 forks acquired, and every one must be closed
	// (discarded) — the per-task hermetic-reset invariant (design 047 §6).
	if b.acquired != 4 || b.closed != 4 {
		t.Fatalf("acquired=%d closed=%d, want 4/4 (every fork discarded)", b.acquired, b.closed)
	}
}
