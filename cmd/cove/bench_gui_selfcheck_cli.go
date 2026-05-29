package main

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"github.com/tmc/cove/internal/guibench"
)

// runBenchGUISelfCheck runs the verifier self-check over a corpus (design 047
// §9 slice 4). For every task it forks a fresh ephemeral VM, runs the task's
// setup plus the known-good solution, and asserts the verifier scores 1.0; then
// it forks again, runs setup only, and asserts the verifier scores 0.0 (the
// AndroidWorld "is the validator correct" discipline). A verifier that cannot
// recognize its own gold solution, or that passes a no-op, is miscalibrated and
// the task is unusable.
//
// The self-check needs a live fork to run, so it is gated like the scored run.
// The corpus still loads and validates without a VM (`cove bench gui validate`),
// and the self-check LOGIC is unit-tested against a fake env; this command is the
// live confirmation an operator runs on Apple-Silicon hardware.
func runBenchGUISelfCheck(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("bench gui selfcheck", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	corpus := fs.String("corpus", "", "task corpus directory (required)")
	subset := fs.String("subset", "", "limit to a named subset, e.g. test_small")
	seed := fs.Uint64("seed", 1, "parameter seed for the materialized variation")
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("bench gui selfcheck: unexpected arguments: %v", fs.Args())
	}
	if *corpus == "" {
		return fmt.Errorf("bench gui selfcheck: -corpus is required")
	}

	tasks, err := guibench.Load(*corpus)
	if err != nil {
		return fmt.Errorf("bench gui selfcheck: %w", err)
	}
	tasks, err = guibench.SelectSubset(tasks, *subset)
	if err != nil {
		return fmt.Errorf("bench gui selfcheck: %w", err)
	}
	if len(tasks) == 0 {
		return fmt.Errorf("bench gui selfcheck: corpus %s has no tasks", *corpus)
	}

	// The base image must carry the grants the corpus's getters need; size the
	// backend to the corpus's MaxTier (design 047 §5).
	imageTier := guibench.MaxTier(tasks)
	backend, err := liveBackend("none", imageTier, env.Stderr)
	if err != nil {
		return fmt.Errorf("bench gui selfcheck: %w", err)
	}

	fmt.Fprintf(env.Stdout, "corpus %s: self-checking %d task(s) at seed %d\n", *corpus, len(tasks), *seed)
	selfCheckEnv := guibench.BackendEnv(context.Background(), backend)
	results, runErr := guibench.SelfCheckCorpus(selfCheckEnv, tasks, *seed)
	for _, r := range results {
		fmt.Fprintln(env.Stdout, r.String())
	}
	if runErr != nil {
		return fmt.Errorf("bench gui selfcheck: %w", runErr)
	}
	fmt.Fprintf(env.Stdout, "all %d task(s) passed the verifier self-check\n", len(results))
	return nil
}

// runBenchGUIExamine runs one task's setup, pauses for a human to act on the
// live guest, then prints the verifier's getter output (OSWorld's
// run_manual_examine shape, design 047 §9 slice 4). It is the tool for
// inspecting GUI-action-to-disk-state lag (§7): perform the GUI action by hand,
// press enter, and see exactly what the getter reads — catching a verifier that
// reads stale state before the corpus relies on it.
func runBenchGUIExamine(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("bench gui examine", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	corpus := fs.String("corpus", "", "task corpus directory (required)")
	taskID := fs.String("task-id", "", "id of the task to examine (required)")
	seed := fs.Uint64("seed", 1, "parameter seed for the materialized variation")
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("bench gui examine: unexpected arguments: %v", fs.Args())
	}
	if *corpus == "" {
		return fmt.Errorf("bench gui examine: -corpus is required")
	}
	if *taskID == "" {
		return fmt.Errorf("bench gui examine: -task-id is required")
	}

	tasks, err := guibench.Load(*corpus)
	if err != nil {
		return fmt.Errorf("bench gui examine: %w", err)
	}
	task := findTaskID(tasks, *taskID)
	if task == nil {
		return fmt.Errorf("bench gui examine: no task %q in corpus %s", *taskID, *corpus)
	}

	backend, err := liveBackend("none", task.Evaluator.Result.Tier(), env.Stderr)
	if err != nil {
		return fmt.Errorf("bench gui examine: %w", err)
	}
	pause := guibench.ReaderPauser{R: env.Stdin, Prompt: env.Stderr}
	selfCheckEnv := guibench.BackendEnv(context.Background(), backend)
	if err := guibench.Examine(selfCheckEnv, task, *seed, pause, env.Stdout); err != nil {
		return fmt.Errorf("bench gui examine: %w", err)
	}
	return nil
}

// findTaskID returns the task with the given id, or nil if none matches.
func findTaskID(tasks []*guibench.Task, id string) *guibench.Task {
	for _, t := range tasks {
		if t.ID == id {
			return t
		}
	}
	return nil
}
