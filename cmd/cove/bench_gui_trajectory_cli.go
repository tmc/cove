package main

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"github.com/tmc/cove/internal/guibench"
)

// runBenchGUIExportTrajectories implements `cove bench gui export-trajectories`
// (design 047 §16): it drops the near-zero-marginal-cost native-macOS
// UI-grounding dataset a benchmark run produces, in a HuggingFace-loadable
// layout (see [guibench.WriteDataset]).
//
// Two modes:
//
//   - --oracle: fork a fresh ephemeral VM per task, run the task's known-good
//     Solution, and record each step as a gold demonstration (reward 1). This is
//     the live path; it needs Apple-Silicon hardware and is the operator command.
//   - scored (default): transform an already-captured run bundle
//     (~/.vz/runs/<id>/ with events.jsonl + screenshots/) into the same schema,
//     with the verifier's score as the reward. This is a pure on-disk transform
//     and needs no VM (the transform itself lives in
//     [guibench.TrajectoryFromBundle], unit-tested without a VM).
func runBenchGUIExportTrajectories(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("bench gui export-trajectories", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	corpus := fs.String("corpus", "", "task corpus directory (required for --oracle)")
	out := fs.String("out", "", "output dataset directory (required)")
	oracle := fs.Bool("oracle", false, "run each task's known-good Solution and export it as a gold demonstration (live, needs a VM)")
	subset := fs.String("subset", "", "limit --oracle to a named subset, e.g. test_small")
	seed := fs.Uint64("seed", 1, "parameter seed for the materialized variation")
	run := fs.String("run", "", "scored mode: run bundle directory to transform (e.g. ~/.vz/runs/<id>)")
	taskID := fs.String("task-id", "", "scored mode: the corpus task id the bundle attempted")
	provider := fs.String("provider", "", "scored mode: the agent that produced the bundle")
	reward := fs.Float64("reward", -1, "scored mode: the verifier score in [0,1] for the bundle")
	instruction := fs.String("instruction", "", "scored mode: the materialized instruction the agent was given")
	domain := fs.String("domain", "", "scored mode: the task domain (optional)")
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("bench gui export-trajectories: unexpected arguments: %v", fs.Args())
	}
	if *out == "" {
		return fmt.Errorf("bench gui export-trajectories: -out is required")
	}

	if *oracle {
		return exportOracleTrajectories(env, *corpus, *subset, *seed, *out)
	}
	return exportScoredTrajectory(env, guibench.BundleSpec{
		Dir:         *run,
		TaskID:      *taskID,
		Provider:    *provider,
		Domain:      *domain,
		Instruction: *instruction,
		Seed:        *seed,
		Reward:      *reward,
	}, *out)
}

// exportOracleTrajectories runs the known-good Solution for every self-checkable
// task on a fresh fork and writes the gold demonstrations as a dataset. It
// reuses the live fork backend, so it shares the scored run's substrate.
func exportOracleTrajectories(env commandEnv, corpus, subset string, seed uint64, out string) error {
	if corpus == "" {
		return fmt.Errorf("bench gui export-trajectories: -corpus is required with -oracle")
	}
	tasks, err := guibench.Load(corpus)
	if err != nil {
		return fmt.Errorf("bench gui export-trajectories: %w", err)
	}
	tasks, err = guibench.SelectSubset(tasks, subset)
	if err != nil {
		return fmt.Errorf("bench gui export-trajectories: %w", err)
	}
	if len(tasks) == 0 {
		return fmt.Errorf("bench gui export-trajectories: corpus %s has no tasks", corpus)
	}

	imageTier := guibench.MaxTier(tasks)
	backend, err := liveBackend("none", imageTier, env.Stderr)
	if err != nil {
		return fmt.Errorf("bench gui export-trajectories: %w", err)
	}

	fmt.Fprintf(env.Stdout, "corpus %s: recording oracle trajectories for %d task(s) at seed %d\n", corpus, len(tasks), seed)
	oracleEnv := guibench.BackendEnv(context.Background(), backend)
	trajs, shots, err := guibench.RecordOracleCorpus(oracleEnv, tasks, seed)
	if err != nil {
		return fmt.Errorf("bench gui export-trajectories: %w", err)
	}
	if err := guibench.WriteDataset(out, trajs, shots, guibench.VerifierVersion()); err != nil {
		return fmt.Errorf("bench gui export-trajectories: %w", err)
	}
	fmt.Fprintf(env.Stdout, "wrote %d oracle trajectory(ies) to %s\n", len(trajs), out)
	return nil
}

// exportScoredTrajectory transforms a captured run bundle into a scored
// trajectory dataset. The transform is pure and lives in guibench; this wires
// flags to it and writes the dataset. No VM is touched.
func exportScoredTrajectory(env commandEnv, spec guibench.BundleSpec, out string) error {
	if spec.Dir == "" {
		return fmt.Errorf("bench gui export-trajectories: -run is required for scored mode (or pass -oracle)")
	}
	if spec.TaskID == "" {
		return fmt.Errorf("bench gui export-trajectories: -task-id is required for scored mode")
	}
	if spec.Reward < 0 || spec.Reward > 1 {
		return fmt.Errorf("bench gui export-trajectories: -reward must be in [0,1] (the verifier score)")
	}
	traj, shots, err := guibench.TrajectoryFromBundle(spec)
	if err != nil {
		return fmt.Errorf("bench gui export-trajectories: %w", err)
	}
	if err := guibench.WriteDataset(out, []*guibench.Trajectory{traj}, shots, guibench.VerifierVersion()); err != nil {
		return fmt.Errorf("bench gui export-trajectories: %w", err)
	}
	fmt.Fprintf(env.Stdout, "wrote scored trajectory (%d step(s), reward %.2f) to %s\n", len(traj.Steps), traj.Reward, out)
	return nil
}
