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

// runFleetHealthCommand probes every registered remote concurrently with a
// short per-host timeout and prints a host/status table (or JSON with --json).
// It is fail-soft: unreachable hosts are reported, not fatal, so a single dead
// host never blocks the sweep.
func runFleetHealthCommand(ctx context.Context, args []string, path string, runner fleetRunner, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("fleet health", flag.ContinueOnError)
	fs.SetOutput(errOut)
	jsonOut := fs.Bool("json", false, "emit JSON")
	timeout := fs.Duration("timeout", fleetpkg.DefaultProbeTimeout, "per-host probe timeout")
	if done, err := parseFlagsOrHelpExit(fs, args); done || err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: cove fleet health [--json] [--timeout d]")
	}
	if runner == nil {
		return errors.New("fleet runner required")
	}
	cfg, err := fleetpkg.LoadPath(path)
	if err != nil {
		return err
	}
	entries := cfg.List()
	if len(entries) == 0 {
		if *jsonOut {
			_, err := io.WriteString(out, "[]\n")
			return err
		}
		fmt.Fprintln(out, "no fleet remotes")
		return nil
	}
	health := fleetpkg.ProbeHosts(ctx, entries, *timeout, func(ctx context.Context, entry fleetpkg.Entry) (string, error) {
		var stdout, stderr bytes.Buffer
		err := runner.Run(ctx, entry.Remote, []string{"vm", "list"}, &stdout, &stderr)
		if err != nil {
			if msg := strings.TrimSpace(stderr.String()); msg != "" {
				return "", fmt.Errorf("%w: %s", err, msg)
			}
			return "", err
		}
		return stdout.String(), nil
	})
	if *jsonOut {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(health)
	}
	if _, err := io.WriteString(out, fleetpkg.FormatHostHealth(health)); err != nil {
		return err
	}
	summary := fleetpkg.SummarizeHealth(health)
	fmt.Fprintf(out, "%d online, %d degraded, %d unreachable\n", summary.Online, summary.Degraded, summary.Unreachable)
	return nil
}
