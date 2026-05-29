package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	fleetpkg "github.com/tmc/cove/internal/fleet"
)

func runFleetMetricsCommand(ctx context.Context, args []string, path string, runner fleetRunner, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("fleet metrics", flag.ContinueOnError)
	fs.SetOutput(errOut)
	jsonOut := fs.Bool("json", false, "emit JSON")
	if done, err := parseFlagsOrHelpExit(fs, args); done || err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: cove fleet metrics [--json]")
	}
	cfg, err := fleetpkg.LoadPath(path)
	if err != nil {
		return err
	}
	result := fleetpkg.FleetMetrics(ctx, cfg.List(), func(ctx context.Context, entry fleetpkg.Entry) (string, error) {
		var stdout, stderr bytes.Buffer
		err := runner.Run(ctx, entry.Remote, []string{"daemon", "metrics", "--json"}, &stdout, &stderr)
		if err != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg != "" {
				return "", fmt.Errorf("%w: %s", err, msg)
			}
			return "", err
		}
		return stdout.String(), nil
	})
	if *jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}
	_, err = io.WriteString(out, fleetpkg.FormatFleetMetrics(result))
	return err
}
