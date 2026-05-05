package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/tmc/vz-macos/internal/runs"
	"github.com/tmc/vz-macos/internal/vmconfig"
)

func handleRunsCommand(args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		printRunsUsage(os.Stdout)
		return nil
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list", "ls":
		return runRunsList(rest)
	case "show":
		return runRunsShow(rest)
	case "export":
		return runRunsExport(rest)
	default:
		printRunsUsage(os.Stderr)
		return fmt.Errorf("unknown runs subcommand: %s", sub)
	}
}

func printRunsUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove runs <subcommand> [options]

Subcommands:
  list [--limit N] [--since D] [--status ok|fail|all] [--json]
  show <run-id-prefix> [--json]
  export <run-id-prefix> --format json|gha-summary|tar`)
}

func runRunsList(args []string) error {
	fs := flag.NewFlagSet("runs list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	limit := fs.Int("limit", 25, "maximum runs to show")
	sinceText := fs.String("since", "", "only show runs started within duration")
	status := fs.String("status", "all", "status filter: ok, fail, all")
	jsonOut := fs.Bool("json", false, "emit NDJSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var since time.Duration
	if *sinceText != "" {
		d, err := time.ParseDuration(*sinceText)
		if err != nil {
			return fmt.Errorf("invalid --since duration: %w", err)
		}
		since = d
	}
	if *status != "ok" && *status != "fail" && *status != "all" {
		return fmt.Errorf("invalid --status %q: want ok, fail, or all", *status)
	}
	summaries, err := runs.List(vmconfig.RunsDir(), runs.Filter{
		Limit:  *limit,
		Since:  since,
		Status: *status,
	})
	if err != nil {
		return err
	}
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		for _, summary := range summaries {
			if err := enc.Encode(summary); err != nil {
				return fmt.Errorf("runs list json: %w", err)
			}
		}
		return nil
	}
	return printRunsTable(os.Stdout, summaries)
}

func runRunsShow(args []string) error {
	prefix, jsonOut, err := parseRunsShowArgs(args)
	if err != nil {
		return err
	}
	if prefix == "" {
		return fmt.Errorf("usage: cove runs show <run-id-prefix> [--json]")
	}
	show, err := runs.LoadShow(vmconfig.RunsDir(), prefix)
	if err != nil {
		return err
	}
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(show.Events)
	}
	return runs.RenderShow(os.Stdout, show)
}

func runRunsExport(args []string) error {
	prefix, format, err := parseRunsExportArgs(args)
	if err != nil {
		return err
	}
	if prefix == "" || format == "" {
		return fmt.Errorf("usage: cove runs export <run-id-prefix> --format json|gha-summary|tar")
	}
	switch format {
	case "json":
		return runs.ExportJSON(os.Stdout, vmconfig.RunsDir(), prefix)
	case "gha-summary":
		return runs.ExportGHASummary(os.Stdout, vmconfig.RunsDir(), prefix)
	case "tar":
		return runs.ExportTarGz(os.Stdout, vmconfig.RunsDir(), prefix)
	default:
		return fmt.Errorf("unknown runs export format %q", format)
	}
}

func parseRunsShowArgs(args []string) (prefix string, jsonOut bool, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json", "-json":
			jsonOut = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return "", false, fmt.Errorf("unknown runs show flag %q", args[i])
			}
			if prefix != "" {
				return "", false, fmt.Errorf("usage: cove runs show <run-id-prefix> [--json]")
			}
			prefix = args[i]
		}
	}
	return prefix, jsonOut, nil
}

func parseRunsExportArgs(args []string) (prefix, format string, err error) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--format" || arg == "-format":
			i++
			if i >= len(args) || args[i] == "" {
				return "", "", fmt.Errorf("runs export: --format requires a value")
			}
			format = args[i]
		case strings.HasPrefix(arg, "--format="):
			format = strings.TrimPrefix(arg, "--format=")
		case strings.HasPrefix(arg, "-format="):
			format = strings.TrimPrefix(arg, "-format=")
		default:
			if strings.HasPrefix(arg, "-") {
				return "", "", fmt.Errorf("unknown runs export flag %q", arg)
			}
			if prefix != "" {
				return "", "", fmt.Errorf("usage: cove runs export <run-id-prefix> --format json|gha-summary|tar")
			}
			prefix = arg
		}
	}
	return prefix, format, nil
}

func printRunsTable(w io.Writer, summaries []runs.Summary) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "RUN ID\tIMAGE\tVM\tSTATUS\tDURATION_MS\tEXIT\tSTARTED_AT")
	for _, summary := range summaries {
		exit := "-"
		if summary.ExitCode != nil {
			exit = fmt.Sprint(*summary.ExitCode)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			shortRunID(summary.RunID),
			emptyDash(summary.ImageRef),
			emptyDash(summary.VMName),
			summary.Status,
			summary.TotalDurationMS,
			exit,
			summary.StartedAt.UTC().Format(time.RFC3339))
	}
	return tw.Flush()
}

func shortRunID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func emptyDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
