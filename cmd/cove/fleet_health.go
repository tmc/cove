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

	fleetpkg "github.com/tmc/cove/internal/fleet"
)

type fleetHealthRow struct {
	Host    string `json:"host"`
	OK      bool   `json:"ok"`
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

func runFleetHealthCommand(ctx context.Context, args []string, path string, runner fleetRunner, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("fleet health", flag.ContinueOnError)
	fs.SetOutput(errOut)
	jsonOut := fs.Bool("json", false, "emit JSON")
	if done, err := parseFlagsOrHelpExit(fs, args); done || err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: cove fleet health [--json]")
	}
	rows, err := queryFleetHealth(ctx, path, runner)
	if err != nil {
		return err
	}
	if *jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	return printFleetHealth(out, rows)
}

func queryFleetHealth(ctx context.Context, path string, runner fleetRunner) ([]fleetHealthRow, error) {
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
		err := runner.Run(ctx, entry.Remote, []string{"version"}, &stdout, &stderr)
		if err != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg != "" {
				return "", fmt.Errorf("%w: %s", err, msg)
			}
			return "", err
		}
		return strings.TrimSpace(stdout.String()), nil
	})
	rows := make([]fleetHealthRow, 0, len(results))
	for _, result := range results {
		row := fleetHealthRow{Host: result.Host, Version: result.Value}
		if result.Error != nil {
			row.Error = result.Error.Error()
		} else {
			row.OK = true
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func printFleetHealth(out io.Writer, rows []fleetHealthRow) error {
	if len(rows) == 0 {
		_, err := fmt.Fprintln(out, "no fleet remotes")
		return err
	}
	for _, row := range rows {
		if row.Error != "" {
			fmt.Fprintf(out, "%s\tunreachable\t%s\n", row.Host, row.Error)
			continue
		}
		fmt.Fprintf(out, "%s\tok\t%s\n", row.Host, row.Version)
	}
	return nil
}
