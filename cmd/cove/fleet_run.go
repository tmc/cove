package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	fleetpkg "github.com/tmc/cove/internal/fleet"
)

type fleetRunProbe struct {
	VMList   string
	HasImage bool
}

func runFleetRunCommand(ctx context.Context, args []string, path string, runner fleetRunner, out, errOut io.Writer) error {
	policy, runArgs, err := extractFleetRunPolicy(args)
	if err != nil {
		return err
	}
	switch policy {
	case "":
		return errors.New("usage: cove fleet run --policy=least-loaded|image-affinity [run flags]")
	case "least-loaded", "image-affinity":
	default:
		return fmt.Errorf("fleet run: unknown policy %q", policy)
	}
	cfg, err := fleetpkg.LoadPath(path)
	if err != nil {
		return err
	}
	entries := cfg.List()
	selected, loads, err := selectFleetRunHost(ctx, policy, runArgs, entries, runner)
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

func selectFleetRunHost(ctx context.Context, policy string, runArgs []string, entries []fleetpkg.Entry, runner fleetRunner) (fleetpkg.Entry, []fleetpkg.HostLoad, error) {
	if policy == "least-loaded" {
		return fleetpkg.SelectLeastLoadedHost(ctx, entries, func(ctx context.Context, entry fleetpkg.Entry) (string, error) {
			return runFleetVMList(ctx, entry, runner)
		})
	}
	imageRef := fleetRunForkFrom(runArgs)
	if imageRef == "" {
		return fleetpkg.Entry{}, nil, errors.New("fleet run: image-affinity requires -fork-from <image-ref>")
	}
	if len(entries) == 0 {
		return fleetpkg.Entry{}, nil, errors.New("fleet placement: no remotes configured")
	}
	results := fleetpkg.QueryAll(ctx, entries, func(ctx context.Context, entry fleetpkg.Entry) (fleetRunProbe, error) {
		vmList, err := runFleetVMList(ctx, entry, runner)
		if err != nil {
			return fleetRunProbe{}, err
		}
		probe := fleetRunProbe{VMList: vmList}
		var stdout, stderr bytes.Buffer
		if err := runner.Run(ctx, entry.Remote, []string{"image", "inspect", "-json", imageRef}, &stdout, &stderr); err == nil {
			probe.HasImage = true
		}
		return probe, nil
	})
	loads := make([]fleetpkg.HostLoad, 0, len(results))
	var candidates []fleetpkg.Entry
	counts := make(map[string]int, len(results))
	for i, result := range results {
		load := fleetpkg.HostLoad{Host: result.Host, Error: result.Error}
		if result.Error == nil {
			load.Count = fleetpkg.CountRunningVMs(result.Value.VMList)
			counts[result.Host] = load.Count
			if result.Value.HasImage {
				candidates = append(candidates, entries[i])
			}
		}
		loads = append(loads, load)
	}
	if len(candidates) == 0 {
		return fleetpkg.Entry{}, loads, fmt.Errorf("fleet placement: no reachable remote has image %s", imageRef)
	}
	sort.Slice(candidates, func(i, j int) bool {
		li, lj := counts[candidates[i].Name], counts[candidates[j].Name]
		if li != lj {
			return li < lj
		}
		return candidates[i].Name < candidates[j].Name
	})
	return candidates[0], loads, nil
}

func fleetRunForkFrom(args []string) string {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-fork-from" || arg == "--fork-from":
			if i+1 < len(args) {
				return strings.TrimSpace(args[i+1])
			}
		case strings.HasPrefix(arg, "-fork-from="):
			return strings.TrimSpace(strings.TrimPrefix(arg, "-fork-from="))
		case strings.HasPrefix(arg, "--fork-from="):
			return strings.TrimSpace(strings.TrimPrefix(arg, "--fork-from="))
		}
	}
	return ""
}

func runFleetVMList(ctx context.Context, entry fleetpkg.Entry, runner fleetRunner) (string, error) {
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
