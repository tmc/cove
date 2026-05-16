package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/tmc/vz-macos/internal/runs"
	"github.com/tmc/vz-macos/internal/vmconfig"
)

type runsShowErrorOutput struct {
	RunID string `json:"run_id"`
	Error string `json:"error"`
}

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
  list [--limit N] [--since D] [--status ok|fail|all] [--json|--ndjson]
  show <run-id-prefix> [--json]
  export <run-id-prefix> --format json|gha-summary|tar [--include-guest /path]`)
}

func runRunsList(args []string) error {
	fs := flag.NewFlagSet("runs list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { printRunsListUsage(fs.Output()) }
	limit := fs.Int("limit", 25, "maximum runs to show")
	sinceText := fs.String("since", "", "only show runs started within duration")
	status := fs.String("status", "all", "status filter: ok, fail, all")
	jsonOut := fs.Bool("json", false, "emit JSON array")
	ndjsonOut := fs.Bool("ndjson", false, "emit NDJSON")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
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
	if *limit < 0 {
		return fmt.Errorf("invalid --limit %d: want non-negative", *limit)
	}
	summaries, err := runs.List(vmconfig.RunsDir(), runs.Filter{
		Limit:  *limit,
		Since:  since,
		Status: *status,
	})
	if err != nil {
		return err
	}
	if *limit == 0 {
		summaries = []runs.Summary{}
	}
	if *jsonOut && *ndjsonOut {
		return fmt.Errorf("runs list: choose only one of --json or --ndjson")
	}
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		return enc.Encode(summaries)
	}
	if *ndjsonOut {
		enc := json.NewEncoder(os.Stdout)
		for _, summary := range summaries {
			if err := enc.Encode(summary); err != nil {
				return fmt.Errorf("runs list ndjson: %w", err)
			}
		}
		return nil
	}
	return printRunsTable(os.Stdout, summaries)
}

func printRunsListUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove runs list [--limit N] [--since D] [--status ok|fail|all] [--json|--ndjson]

List local run metrics from ~/.vz/runs.

Flags:
  --limit N              maximum runs to show (default 25)
  --since D              only show runs started within duration, for example 24h
  --status ok|fail|all   filter by final status (default all)
  --json                 emit a JSON array
  --ndjson               emit one JSON object per run`)
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
		if jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			if jsonErr := enc.Encode(runsShowErrorOutput{
				RunID: prefix,
				Error: err.Error(),
			}); jsonErr != nil {
				return jsonErr
			}
		}
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
	return runRunsExportWith(context.Background(), args, vmconfig.RunsDir(), os.Stdout, newControlCpAgent)
}

func runRunsExportWith(ctx context.Context, args []string, root string, out io.Writer, newAgent func(string) cpAgent) error {
	prefix, format, guestPaths, err := parseRunsExportArgs(args)
	if err != nil {
		return err
	}
	if prefix == "" || format == "" {
		return fmt.Errorf("usage: cove runs export <run-id-prefix> --format json|gha-summary|tar [--include-guest /path]")
	}
	if len(guestPaths) > 0 {
		if format != "tar" {
			return fmt.Errorf("runs export: --include-guest requires --format tar")
		}
		if err := includeGuestArtifacts(ctx, root, prefix, guestPaths, newAgent); err != nil {
			return err
		}
	}
	switch format {
	case "json":
		return runs.ExportJSON(out, root, prefix)
	case "gha-summary":
		return runs.ExportGHASummary(out, root, prefix)
	case "tar":
		return runs.ExportTarGz(out, root, prefix)
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

func parseRunsExportArgs(args []string) (prefix, format string, guestPaths []string, err error) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--format" || arg == "-format":
			i++
			if i >= len(args) || args[i] == "" {
				return "", "", nil, fmt.Errorf("runs export: --format requires a value")
			}
			format = args[i]
		case strings.HasPrefix(arg, "--format="):
			format = strings.TrimPrefix(arg, "--format=")
		case strings.HasPrefix(arg, "-format="):
			format = strings.TrimPrefix(arg, "-format=")
		case arg == "--include-guest" || arg == "-include-guest":
			i++
			if i >= len(args) || args[i] == "" {
				return "", "", nil, fmt.Errorf("runs export: --include-guest requires a guest path")
			}
			guestPaths = append(guestPaths, args[i])
		case strings.HasPrefix(arg, "--include-guest="):
			guestPaths = append(guestPaths, strings.TrimPrefix(arg, "--include-guest="))
		case strings.HasPrefix(arg, "-include-guest="):
			guestPaths = append(guestPaths, strings.TrimPrefix(arg, "-include-guest="))
		default:
			if strings.HasPrefix(arg, "-") {
				return "", "", nil, fmt.Errorf("unknown runs export flag %q", arg)
			}
			if prefix != "" {
				return "", "", nil, fmt.Errorf("usage: cove runs export <run-id-prefix> --format json|gha-summary|tar [--include-guest /path]")
			}
			prefix = arg
		}
	}
	return prefix, format, guestPaths, nil
}

func includeGuestArtifacts(ctx context.Context, root, prefix string, guestPaths []string, newAgent func(string) cpAgent) error {
	show, err := runs.LoadShow(root, prefix)
	if err != nil {
		return err
	}
	vm := runExportVMName(show)
	if vm == "" {
		return fmt.Errorf("runs export: run %s has no vm_name in metrics", show.RunID)
	}
	hostPaths := make([]string, len(guestPaths))
	for i, guestPath := range guestPaths {
		hostPath, err := guestArtifactHostPath(show.Dir, guestPath)
		if err != nil {
			return err
		}
		hostPaths[i] = hostPath
	}
	agent := newAgent(vm)
	for i, guestPath := range guestPaths {
		hostPath := hostPaths[i]
		if err := os.MkdirAll(filepath.Dir(hostPath), 0755); err != nil {
			return fmt.Errorf("runs export: prepare guest artifact path: %w", err)
		}
		if err := agent.CopyFromGuest(ctx, filepath.Clean(guestPath), hostPath); err != nil {
			return fmt.Errorf("runs export: copy guest %s: %w", guestPath, err)
		}
	}
	return nil
}

func runExportVMName(show runs.Show) string {
	for _, event := range show.Events {
		if event.VMName != "" {
			return event.VMName
		}
	}
	return ""
}

func guestArtifactHostPath(runDir, guestPath string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(guestPath))
	if !filepath.IsAbs(clean) {
		return "", fmt.Errorf("runs export: guest path %q must be absolute", guestPath)
	}
	rel := strings.TrimPrefix(clean, string(filepath.Separator))
	if rel == "" || rel == "." {
		return "", fmt.Errorf("runs export: guest path %q does not name a file", guestPath)
	}
	return filepath.Join(runDir, "guest", rel), nil
}

func printRunsTable(w io.Writer, summaries []runs.Summary) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "RUN ID\tIMAGE\tVM\tSTATUS\tDURATION_MS\tEVENTS\tEXIT\tSTARTED_AT")
	for _, summary := range summaries {
		exit := "-"
		if summary.ExitCode != nil {
			exit = fmt.Sprint(*summary.ExitCode)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%d\t%s\t%s\n",
			shortRunID(summary.RunID),
			emptyDash(summary.ImageRef),
			emptyDash(summary.VMName),
			summary.Status,
			summary.TotalDurationMS,
			summary.EventCount,
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
