package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/tmc/vz-macos/internal/bench"
	"github.com/tmc/vz-macos/internal/vmconfig"
)

func handleBenchCommand(args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		printBenchUsage()
		return nil
	}
	switch args[0] {
	case "competitive":
		return runBenchCompetitive(args[1:])
	default:
		printBenchUsage()
		return fmt.Errorf("unknown bench subcommand: %s", args[0])
	}
}

func printBenchUsage() {
	fmt.Fprintln(os.Stderr, `Usage: cove bench <subcommand> [options]

Subcommands:
  competitive   Normalize checked-in competitive benchmark evidence`)
}

func runBenchCompetitive(args []string) error {
	fs := flag.NewFlagSet("bench competitive", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	out := fs.String("out", "docs/benchmarks/results-2026-05-cove.json", "write normalized JSON report")
	markdown := fs.String("markdown", "docs/benchmarks/competitive-2026-05.md", "write Markdown summary")
	jsonOut := fs.Bool("json", false, "also print JSON report to stdout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("bench competitive: unexpected arguments: %v", fs.Args())
	}
	report, err := bench.RunCompetitive(context.Background(), bench.CompetitiveConfig{
		RepoRoot:     ".",
		OutPath:      *out,
		MarkdownPath: *markdown,
		RunsRoot:     vmconfig.RunsDir(),
	})
	if err != nil {
		return err
	}
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	fmt.Fprintf(os.Stdout, "benchmark report: %s\n", *out)
	fmt.Fprintf(os.Stdout, "benchmark summary: %s\n", *markdown)
	fmt.Fprintf(os.Stdout, "run id: %s\n", report.RunID)
	return nil
}
