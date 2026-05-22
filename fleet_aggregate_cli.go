package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	fleetpkg "github.com/tmc/cove/internal/fleet"
)

type fleetAggregateRow struct {
	Host   string `json:"host"`
	Kind   string `json:"kind"`
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

func runFleetAggregateCommand(ctx context.Context, args []string, path string, runner fleetRunner, out, errOut io.Writer) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: cove fleet %s ls [--json]", args[0])
	}
	kind := args[0]
	sub := args[1]
	if kind == "image" {
		switch sub {
		case "push", "pull", "sync":
			return runFleetImageTransferCommand(ctx, args[1:], path, runner, out, errOut)
		}
	}
	if sub != "ls" && sub != "list" {
		return fmt.Errorf("fleet: unknown %s command %q", kind, sub)
	}
	fs := flag.NewFlagSet("fleet "+kind+" ls", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	jsonOut := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: cove fleet %s ls [--json]", kind)
	}
	var remoteArgs []string
	switch kind {
	case "vm":
		remoteArgs = []string{"vm", "list"}
	case "image":
		remoteArgs = []string{"image", "list"}
	default:
		return fmt.Errorf("fleet: unsupported aggregate kind %q", kind)
	}
	rows, err := queryFleetText(ctx, path, runner, remoteArgs, kind)
	if err != nil {
		return err
	}
	if *jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	return printFleetAggregate(out, rows)
}

func queryFleetText(ctx context.Context, path string, runner fleetRunner, args []string, kind string) ([]fleetAggregateRow, error) {
	if runner == nil {
		return nil, errors.New("fleet runner required")
	}
	cfg, err := fleetpkg.LoadPath(path)
	if err != nil {
		return nil, err
	}
	entries := cfg.List()
	if len(entries) == 0 {
		return nil, nil
	}
	results := fleetpkg.QueryAll(ctx, entries, func(ctx context.Context, entry fleetpkg.Entry) (string, error) {
		var stdout, stderr bytes.Buffer
		err := runner.Run(ctx, entry.Remote, args, &stdout, &stderr)
		if err != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg != "" {
				return "", fmt.Errorf("%w: %s", err, msg)
			}
			return "", err
		}
		return strings.TrimRight(stdout.String(), "\n"), nil
	})
	rows := make([]fleetAggregateRow, 0, len(results))
	for _, r := range results {
		row := fleetAggregateRow{Host: r.Host, Kind: kind, Output: r.Value}
		if r.Error != nil {
			row.Error = r.Error.Error()
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func printFleetAggregate(out io.Writer, rows []fleetAggregateRow) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(out, "no fleet remotes")
		return err
	}
	for _, row := range rows {
		if row.Error != "" {
			fmt.Fprintf(out, "%s\t(unreachable)\t%s\n", row.Host, row.Error)
			continue
		}
		if strings.TrimSpace(row.Output) == "" {
			fmt.Fprintf(out, "%s\t(no results)\n", row.Host)
			continue
		}
		for _, line := range strings.Split(row.Output, "\n") {
			fmt.Fprintf(out, "%s\t%s\n", row.Host, line)
		}
	}
	return nil
}

func runFleetPSCommand(ctx context.Context, args []string, path string, runner fleetRunner, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("fleet ps", flag.ContinueOnError)
	fs.SetOutput(errOut)
	jsonOut := fs.Bool("json", false, "emit JSON")
	watch := fs.Bool("watch", false, "refresh until interrupted")
	if done, err := parseFlagsOrHelpExit(fs, args); done || err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: cove fleet ps [--json] [--watch]")
	}
	render := func() error {
		rows, err := queryFleetText(ctx, path, runner, []string{"vm", "list"}, "ps")
		if err != nil {
			return err
		}
		rows = filterFleetRunningVMs(rows)
		if *jsonOut {
			enc := json.NewEncoder(out)
			enc.SetIndent("", "  ")
			return enc.Encode(rows)
		}
		return printFleetAggregate(out, rows)
	}
	if !*watch {
		return render()
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if err := render(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			fmt.Fprintln(out)
		}
	}
}

func filterFleetRunningVMs(rows []fleetAggregateRow) []fleetAggregateRow {
	out := make([]fleetAggregateRow, 0, len(rows))
	for _, row := range rows {
		if row.Error != "" {
			out = append(out, row)
			continue
		}
		var lines []string
		for _, line := range strings.Split(row.Output, "\n") {
			if fleetLineIsRunningVM(line) {
				lines = append(lines, line)
			}
		}
		row.Output = strings.Join(lines, "\n")
		out = append(out, row)
	}
	return out
}

func fleetLineIsRunningVM(line string) bool {
	return strings.Contains(strings.ToLower(line), "running")
}
