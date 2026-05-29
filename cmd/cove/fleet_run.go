package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	fleetpkg "github.com/tmc/cove/internal/fleet"
)

func runFleetRunCommand(ctx context.Context, args []string, path string, runner fleetRunner, out, errOut io.Writer) error {
	all, args := extractFleetRunAll(args)
	policy, runArgs, err := extractFleetRunPolicy(args)
	if err != nil {
		return err
	}
	if all && policy == "" {
		policy = "fan-out"
	}
	switch policy {
	case "":
		return errors.New("usage: cove fleet run --policy=least-loaded|fan-out [run flags] (or --all)")
	case "fan-out", "spread", "all":
		return runFleetFanOut(ctx, runArgs, path, runner, out, errOut)
	case "least-loaded":
	default:
		return fmt.Errorf("fleet run: unknown policy %q", policy)
	}
	cfg, err := fleetpkg.LoadPath(path)
	if err != nil {
		return err
	}
	entries := cfg.List()
	selected, loads, err := fleetpkg.SelectLeastLoadedHost(ctx, entries, func(ctx context.Context, entry fleetpkg.Entry) (string, error) {
		var stdout, stderr bytes.Buffer
		err := runner.Run(ctx, entry.Remote, []string{"vm", "list"}, &stdout, &stderr)
		if err != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg != "" {
				return "", fmt.Errorf("%w: %s", err, msg)
			}
			return "", err
		}
		return stdout.String(), nil
	})
	if err != nil {
		return err
	}
	if summary := fleetpkg.FormatHostLoads(loads); summary != "" {
		fmt.Fprintf(out, "selected %s (%s)\n", selected.Name, summary)
	} else {
		fmt.Fprintf(out, "selected %s\n", selected.Name)
	}
	return runner.Run(ctx, selected.Remote, append([]string{"run"}, runArgs...), out, errOut)
}

// extractFleetRunAll strips a leading-or-embedded --all/-all flag from args and
// reports whether it was present. The flag selects the fan-out policy.
func extractFleetRunAll(args []string) (bool, []string) {
	all := false
	rest := make([]string, 0, len(args))
	for _, arg := range args {
		switch arg {
		case "--all", "-all":
			all = true
		default:
			rest = append(rest, arg)
		}
	}
	return all, rest
}

// runFleetFanOut issues the run concurrently to every registered remote and
// prints a fail-soft per-host summary, mirroring the aggregate commands: one
// row per host with ok/failed status and the failure reason for non-OK hosts.
func runFleetFanOut(ctx context.Context, runArgs []string, path string, runner fleetRunner, out, errOut io.Writer) error {
	cfg, err := fleetpkg.LoadPath(path)
	if err != nil {
		return err
	}
	entries := cfg.List()
	if len(entries) == 0 {
		fmt.Fprintln(out, "no fleet remotes")
		return nil
	}
	res := fleetpkg.FanOut(ctx, entries, 0, func(ctx context.Context, entry fleetpkg.Entry) (string, error) {
		var stdout, stderr bytes.Buffer
		err := runner.Run(ctx, entry.Remote, append([]string{"run"}, runArgs...), &stdout, &stderr)
		if err != nil {
			if msg := strings.TrimSpace(stderr.String()); msg != "" {
				return "", fmt.Errorf("%w: %s", err, msg)
			}
			return "", err
		}
		return stdout.String(), nil
	})
	for _, o := range res.Outcomes {
		if !o.OK {
			fmt.Fprintf(out, "%s\t(error)\t%s\n", o.Host, o.Error)
			continue
		}
		if strings.TrimSpace(o.Output) == "" {
			fmt.Fprintf(out, "%s\tok\n", o.Host)
			continue
		}
		fmt.Fprintf(out, "%s\tok\t%s\n", o.Host, firstLine(o.Output))
	}
	fmt.Fprintf(out, "fan-out: %d ok, %d failed\n", res.Success, res.Failed)
	if res.Failed > 0 {
		return fmt.Errorf("fleet run: %d of %d hosts failed", res.Failed, len(entries))
	}
	return nil
}

func extractFleetRunPolicy(args []string) (string, []string, error) {
	var policy string
	runArgs := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--policy" || arg == "-policy":
			if i+1 >= len(args) {
				return "", nil, errors.New("fleet run: --policy requires a value")
			}
			policy = args[i+1]
			i++
		case strings.HasPrefix(arg, "--policy="):
			policy = strings.TrimPrefix(arg, "--policy=")
		case strings.HasPrefix(arg, "-policy="):
			policy = strings.TrimPrefix(arg, "-policy=")
		default:
			runArgs = append(runArgs, arg)
		}
	}
	return policy, runArgs, nil
}
