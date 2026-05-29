package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/tmc/cove/internal/bench"
	"github.com/tmc/cove/internal/vmconfig"
)

func handleBenchCommand(env commandEnv, args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		printBenchUsage(env.Stderr)
		return nil
	}
	switch args[0] {
	case "competitive":
		return runBenchCompetitive(env, args[1:])
	default:
		printBenchUsage(env.Stderr)
		return fmt.Errorf("unknown bench subcommand: %s", args[0])
	}
}

func printBenchUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove bench <subcommand> [options]

Subcommands:
  competitive   Normalize checked-in competitive benchmark evidence

Safe example:
  cove bench competitive -dry-run -json`)
}

func runBenchCompetitive(env commandEnv, args []string) error {
	fs := flag.NewFlagSet("bench competitive", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	out := fs.String("out", "docs/benchmarks/results-2026-05-cove.json", "write normalized JSON report")
	markdown := fs.String("markdown", "docs/benchmarks/competitive-2026-05.md", "write Markdown summary")
	jsonOut := fs.Bool("json", false, "also print JSON report to stdout")
	stdout := fs.Bool("stdout", false, "print JSON report to stdout without writing files")
	dryRun := fs.Bool("dry-run", false, "build the report without writing files or run metrics")
	if err := parseFlagsOrHelp(fs, args); err != nil {
		if errors.Is(err, errFlagHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("bench competitive: unexpected arguments: %v", fs.Args())
	}
	outPath, markdownPath, runsRoot := *out, *markdown, vmconfig.RunsDir()
	if *dryRun || *stdout {
		outPath, markdownPath, runsRoot = "", "", ""
	}
	report, err := bench.RunCompetitive(context.Background(), bench.CompetitiveConfig{
		RepoRoot:     ".",
		OutPath:      outPath,
		MarkdownPath: markdownPath,
		RunsRoot:     runsRoot,
	})
	if err != nil {
		return err
	}
	if *jsonOut || *stdout {
		enc := json.NewEncoder(env.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	if *dryRun {
		fmt.Fprintf(env.Stdout, "benchmark dry run: %d result(s)\n", len(report.Results))
		return nil
	}
	fmt.Fprintf(env.Stdout, "benchmark report: %s\n", *out)
	fmt.Fprintf(env.Stdout, "benchmark summary: %s\n", *markdown)
	fmt.Fprintf(env.Stdout, "run id: %s\n", report.RunID)
	return nil
}
