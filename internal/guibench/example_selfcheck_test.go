package guibench_test

import (
	"fmt"

	"github.com/tmc/cove/internal/guibench"
)

// solvedGuest is a tiny stateful fake guest: the solution step "solve" marks it
// solved, and the getter command "probe" reports the state. A fresh one is
// handed out per run, so the no-op run never sees the solution run's state.
type solvedGuest struct{ solved bool }

func (g *solvedGuest) Exec(args []string, _ map[string]string, _ string) (int, string, string, error) {
	if len(args) == 1 && args[0] == "solve" {
		g.solved = true
	}
	if len(args) == 1 && args[0] == "probe" {
		if g.solved {
			return 0, "true", "", nil
		}
		return 0, "false", "", nil
	}
	return 0, "", "", nil
}
func (g *solvedGuest) ReadFile(string) ([]byte, error) { return nil, nil }
func (g *solvedGuest) OCRAllText() (string, error)     { return "", nil }
func (g *solvedGuest) Probe() guibench.Probe           { return g }
func (g *solvedGuest) Close() error                    { return nil }

type solvedEnv struct{}

func (solvedEnv) Acquire(string) (guibench.SelfCheckSession, error) { return &solvedGuest{}, nil }

// ExampleSelfCheck verifies a task's gold solution scores 1 and a no-op scores
// 0 — the AndroidWorld "is the validator correct" discipline. It runs against a
// fake env, so it needs no VM.
func ExampleSelfCheck() {
	task := &guibench.Task{
		ID:        "demo",
		Image:     "macos-base:v1",
		Config:    []guibench.SetupStep{{Args: []string{"reset"}}},
		Solution:  []guibench.SetupStep{{Args: []string{"solve"}}},
		Evaluator: guibench.Evaluator{Func: guibench.StringList{"file_exists"}, Result: guibench.GetterSpec{Kind: "exec", Args: []string{"probe"}}},
	}
	r := guibench.SelfCheck(solvedEnv{}, task, 1)
	fmt.Printf("good=%.0f noop=%.0f ok=%v\n", r.Good, r.NoOp, r.OK)
	// Output: good=1 noop=0 ok=true
}
