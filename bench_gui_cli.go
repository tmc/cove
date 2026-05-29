package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"sort"

	"github.com/tmc/cove/internal/guibench"
)

// handleBenchGUI dispatches `cove bench gui` subcommands. Slice 1 ships the
// VM-free surface: corpus validation and metric listing. The scored run lands
// in slice 2.
func runBenchGUI(env commandEnv, args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		printBenchGUIUsage(env.Stderr)
		return nil
	}
	switch args[0] {
	case "validate":
		return runBenchGUIValidate(env, args[1:])
	case "metrics":
		return runBenchGUIMetrics(env, args[1:])
	case "manifest":
		return runBenchGUIManifest(env, args[1:])
	case "verify-bundle":
		return runBenchGUIVerifyBundle(env, args[1:])
	case "image-check":
		return runBenchGUIImageCheck(env, args[1:])
	case "run":
		return runBenchGUIRun(env, args[1:])
	case "report":
		return runBenchGUIReport(env, args[1:])
	case "selfcheck":
		return runBenchGUISelfCheck(env, args[1:])
	case "examine":
		return runBenchGUIExamine(env, args[1:])
	case "export-trajectories":
		return runBenchGUIExportTrajectories(env, args[1:])
	case "view":
		return runBenchGUIView(env, args[1:])
	default:
		printBenchGUIUsage(env.Stderr)
		return fmt.Errorf("unknown bench gui subcommand: %s", args[0])
	}
}

func printBenchGUIUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove bench gui <subcommand> [options]

Subcommands:
  validate       Load and validate a task corpus (-corpus <dir>)
  metrics        List registered verifier metrics
  manifest       Print the versioned corpus manifest (-corpus <dir>)
  verify-bundle  Validate a result bundle and stamp its tier (-corpus <dir> [-maintainer] <bundle>)
  image-check    Verify a candidate base image carries the corpus's TCC grants (-vm <fork> [-corpus <dir>])
  run            Score a corpus across providers (-corpus <dir> -providers a,b,c [-runs N] [-subset test_small] [-report <path>])
  report         Render an existing score.json into the citable table (-in score.json [-markdown <path>])
  selfcheck      Verify each task's gold solution scores 1.0 and a no-op scores 0.0 (-corpus <dir> [-subset test_small])
  examine        Run a task's setup, pause for manual action, then print the verifier state (-corpus <dir> -task-id <id>)
  export-trajectories  Export oracle (gold) or scored-run trajectories as a HuggingFace-loadable dataset (-out <dir> [-oracle -corpus <dir>] | [-run <bundle> -task-id <id> -reward <0..1>])
  view           Render a run bundle as a local HTML timeline (<run-dir> | <run-id-prefix>) [-stdout]

Example:
  cove bench gui validate -corpus docs/benchmarks/gui-corpus
  cove bench gui manifest -corpus docs/benchmarks/gui-corpus
  cove bench gui run -corpus docs/benchmarks/gui-corpus -providers anthropic,openai,gemini -runs 3 -subset test_small -report bench/gui/run
  cove bench gui report -in bench/gui/run/score.json
  cove bench gui selfcheck -corpus internal/guibench/testdata/corpus-v0
  cove bench gui examine -corpus internal/guibench/testdata/corpus-v0 -task-id finder-create-folder
  cove bench gui export-trajectories -oracle -corpus internal/guibench/testdata/corpus-v0 -out bench/gui/oracle-dataset
  cove bench gui export-trajectories -run ~/.vz/runs/ab12cd34 -task-id finder-create-folder -reward 1 -out bench/gui/scored-dataset
  cove bench gui view ~/.vz/runs/deadbeef`)
}

func runBenchGUIValidate(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("bench gui validate", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	corpus := fs.String("corpus", "", "task corpus directory to load and validate")
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("bench gui validate: unexpected arguments: %v", fs.Args())
	}
	if *corpus == "" {
		return fmt.Errorf("bench gui validate: -corpus is required")
	}
	tasks, err := guibench.Load(*corpus)
	if err != nil {
		return fmt.Errorf("bench gui validate: %w", err)
	}
	fmt.Fprintf(env.Stdout, "corpus %s: %d task(s) valid\n", *corpus, len(tasks))
	for _, t := range tasks {
		fmt.Fprintf(env.Stdout, "  %s\n", t.ID)
	}
	return nil
}

func runBenchGUIMetrics(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("bench gui metrics", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("bench gui metrics: unexpected arguments: %v", fs.Args())
	}
	names := make([]string, 0)
	for name := range guibench.Metrics() {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintln(env.Stdout, name)
	}
	return nil
}
