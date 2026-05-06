package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	fleetpkg "github.com/tmc/vz-macos/internal/fleet"
)

func runFleetRunCommand(ctx context.Context, args []string, path string, runner fleetRunner, out, errOut io.Writer) error {
	policy, runArgs, err := extractFleetRunPolicy(args)
	if err != nil {
		return err
	}
	switch policy {
	case "":
		return errors.New("usage: cove fleet run --policy=least-loaded [run flags]")
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
