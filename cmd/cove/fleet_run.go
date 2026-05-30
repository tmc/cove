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

type fleetRunPlacement struct {
	Selected fleetpkg.Entry
	Loads    []fleetpkg.HostLoad
	Prestage *fleetRunPrestage
}

type fleetRunPrestage struct {
	Ref         string
	SourceName  string
	Source      fleetpkg.Remote
	Destination fleetpkg.Entry
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
	placement, err := planFleetRunPlacement(ctx, policy, runArgs, entries, runner)
	if err != nil {
		return err
	}
	if placement.Prestage != nil {
		stage := placement.Prestage
		if err := fleetpkg.TransferImage(ctx, stage.Ref, stage.Source, stage.Destination.Remote, fleetImageCommandRunner(runner)); err != nil {
			return err
		}
		fmt.Fprintf(out, "staged image %s from %s to %s\n", stage.Ref, stage.SourceName, stage.Destination.Name)
	}
	selected, loads := placement.Selected, placement.Loads
	if summary := fleetpkg.FormatHostLoads(loads); summary != "" {
		fmt.Fprintf(out, "selected %s (%s)\n", selected.Name, summary)
	} else {
		fmt.Fprintf(out, "selected %s\n", selected.Name)
	}
	return runner.Run(ctx, selected.Remote, append([]string{"run"}, runArgs...), out, errOut)
}

func selectFleetRunHost(ctx context.Context, policy string, runArgs []string, entries []fleetpkg.Entry, runner fleetRunner) (fleetpkg.Entry, []fleetpkg.HostLoad, error) {
	placement, err := planFleetRunPlacement(ctx, policy, runArgs, entries, runner)
	return placement.Selected, placement.Loads, err
}

func planFleetRunPlacement(ctx context.Context, policy string, runArgs []string, entries []fleetpkg.Entry, runner fleetRunner) (fleetRunPlacement, error) {
	if policy == "least-loaded" {
		selected, loads, err := fleetpkg.SelectLeastLoadedHost(ctx, entries, func(ctx context.Context, entry fleetpkg.Entry) (string, error) {
			return runFleetVMList(ctx, entry, runner)
		})
		return fleetRunPlacement{Selected: selected, Loads: loads}, err
	}
	imageRef := fleetRunForkFrom(runArgs)
	if imageRef == "" {
		return fleetRunPlacement{}, errors.New("fleet run: image-affinity requires -fork-from <image-ref>")
	}
	if len(entries) == 0 {
		return fleetRunPlacement{}, errors.New("fleet placement: no remotes configured")
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
	var reachable []fleetpkg.Entry
	counts := make(map[string]int, len(results))
	for i, result := range results {
		load := fleetpkg.HostLoad{Host: result.Host, Error: result.Error}
		if result.Error == nil {
			load.Count = fleetpkg.CountRunningVMs(result.Value.VMList)
			counts[result.Host] = load.Count
			reachable = append(reachable, entries[i])
			if result.Value.HasImage {
				candidates = append(candidates, entries[i])
			}
		}
		loads = append(loads, load)
	}
	if len(candidates) > 0 {
		return fleetRunPlacement{Selected: leastLoadedFleetRunEntry(candidates, counts), Loads: loads}, nil
	}
	if len(reachable) == 0 {
		return fleetRunPlacement{Loads: loads}, errors.New("fleet placement: all remotes unreachable")
	}
	selected := leastLoadedFleetRunEntry(reachable, counts)
	return fleetRunPlacement{
		Selected: selected,
		Loads:    loads,
		Prestage: &fleetRunPrestage{
			Ref:         imageRef,
			SourceName:  "local",
			Source:      fleetpkg.Remote{},
			Destination: selected,
		},
	}, nil
}

func leastLoadedFleetRunEntry(entries []fleetpkg.Entry, counts map[string]int) fleetpkg.Entry {
	sort.Slice(entries, func(i, j int) bool {
		li, lj := counts[entries[i].Name], counts[entries[j].Name]
		if li != lj {
			return li < lj
		}
		return entries[i].Name < entries[j].Name
	})
	return entries[0]
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
