package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

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

type fleetRunOptions struct {
	Policy  string
	All     bool
	RunArgs []string
}

type fleetRunAllResult struct {
	Stdout string
	Stderr string
}

func runFleetRunCommand(ctx context.Context, args []string, path string, runner fleetRunner, out, errOut io.Writer) error {
	opts, err := extractFleetRunOptions(args)
	if err != nil {
		return err
	}
	if opts.All {
		if opts.Policy != "" {
			return errors.New("fleet run: --all cannot be combined with --policy")
		}
		return runFleetRunAllCommand(ctx, opts.RunArgs, path, runner, out, errOut)
	}
	switch opts.Policy {
	case "":
		return errors.New("usage: cove fleet run --policy=least-loaded|image-affinity|--all [run flags]")
	case "least-loaded", "image-affinity":
	default:
		return fmt.Errorf("fleet run: unknown policy %q", opts.Policy)
	}
	cfg, err := fleetpkg.LoadPath(path)
	if err != nil {
		return err
	}
	entries := cfg.List()
	leases, err := fleetpkg.ActivePlacementLeaseCounts(path, time.Now())
	if err != nil {
		return err
	}
	placement, err := planFleetRunPlacement(ctx, opts.Policy, opts.RunArgs, entries, leases, runner)
	if err != nil {
		return err
	}
	if err := fleetpkg.RecordPlacementLease(path, placement.Selected.Name, time.Now(), fleetpkg.DefaultPlacementLeaseTTL); err != nil {
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
	return runner.Run(ctx, selected.Remote, append([]string{"run"}, opts.RunArgs...), out, errOut)
}

func runFleetRunAllCommand(ctx context.Context, runArgs []string, path string, runner fleetRunner, out, errOut io.Writer) error {
	if runner == nil {
		return errors.New("fleet runner required")
	}
	cfg, err := fleetpkg.LoadPath(path)
	if err != nil {
		return err
	}
	entries := cfg.List()
	if len(entries) == 0 {
		return errors.New("fleet placement: no remotes configured")
	}
	active, skipped := fleetpkg.ActivePlacementEntries(entries)
	if len(active) == 0 {
		return errors.New("fleet placement: all remotes cordoned")
	}
	now := time.Now()
	for _, entry := range active {
		if err := fleetpkg.RecordPlacementLease(path, entry.Name, now, fleetpkg.DefaultPlacementLeaseTTL); err != nil {
			return err
		}
	}
	if imageRef := fleetRunForkFrom(runArgs); imageRef != "" {
		if err := prestageFleetRunAllImage(ctx, imageRef, active, runner, out); err != nil {
			return err
		}
	}
	names := make([]string, 0, len(active))
	for _, entry := range active {
		names = append(names, entry.Name)
	}
	fmt.Fprintf(out, "running on %s\n", strings.Join(names, ", "))
	if summary := fleetpkg.FormatHostLoads(skipped); summary != "" {
		fmt.Fprintf(out, "skipped %s\n", summary)
	}
	results := fleetpkg.QueryAll(ctx, active, func(ctx context.Context, entry fleetpkg.Entry) (fleetRunAllResult, error) {
		var stdout, stderr bytes.Buffer
		err := runner.Run(ctx, entry.Remote, append([]string{"run"}, runArgs...), &stdout, &stderr)
		result := fleetRunAllResult{Stdout: stdout.String(), Stderr: stderr.String()}
		if err != nil {
			msg := strings.TrimSpace(stderr.String())
			if msg != "" {
				return result, fmt.Errorf("%w: %s", err, msg)
			}
			return result, err
		}
		return result, nil
	})
	failures := 0
	for _, result := range results {
		writeFleetRunLines(out, result.Host, "", result.Value.Stdout)
		writeFleetRunLines(errOut, result.Host, "stderr", result.Value.Stderr)
		if result.Error != nil {
			failures++
			fmt.Fprintf(out, "%s\terror\t%s\n", result.Host, result.Error)
			continue
		}
		fmt.Fprintf(out, "%s\tok\n", result.Host)
	}
	if failures > 0 {
		return fmt.Errorf("fleet run --all: %d host(s) failed", failures)
	}
	return nil
}

func prestageFleetRunAllImage(ctx context.Context, imageRef string, entries []fleetpkg.Entry, runner fleetRunner, out io.Writer) error {
	commandRunner := fleetImageCommandRunner(runner)
	for _, entry := range entries {
		var stdout, stderr bytes.Buffer
		if err := runner.Run(ctx, entry.Remote, []string{"image", "inspect", "-json", imageRef}, &stdout, &stderr); err == nil {
			continue
		}
		if err := fleetpkg.TransferImage(ctx, imageRef, fleetpkg.Remote{}, entry.Remote, commandRunner); err != nil {
			return fmt.Errorf("stage image %s to %s: %w", imageRef, entry.Name, err)
		}
		fmt.Fprintf(out, "staged image %s from local to %s\n", imageRef, entry.Name)
	}
	return nil
}

func selectFleetRunHost(ctx context.Context, policy string, runArgs []string, entries []fleetpkg.Entry, runner fleetRunner) (fleetpkg.Entry, []fleetpkg.HostLoad, error) {
	placement, err := planFleetRunPlacement(ctx, policy, runArgs, entries, nil, runner)
	return placement.Selected, placement.Loads, err
}

func planFleetRunPlacement(ctx context.Context, policy string, runArgs []string, entries []fleetpkg.Entry, leases map[string]int, runner fleetRunner) (fleetRunPlacement, error) {
	if policy == "least-loaded" {
		selected, loads, err := fleetpkg.SelectLeastLoadedHostWithLeases(ctx, entries, leases, func(ctx context.Context, entry fleetpkg.Entry) (string, error) {
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
	active, loads := fleetpkg.ActivePlacementEntries(entries)
	if len(active) == 0 {
		return fleetRunPlacement{Loads: loads}, errors.New("fleet placement: all remotes cordoned")
	}
	results := fleetpkg.QueryAll(ctx, active, func(ctx context.Context, entry fleetpkg.Entry) (fleetRunProbe, error) {
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
	var candidates []fleetpkg.Entry
	var reachable []fleetpkg.Entry
	counts := make(map[string]int, len(results))
	for i, result := range results {
		load := fleetpkg.HostLoad{Host: result.Host, Error: result.Error}
		if result.Error == nil {
			load.Count = fleetpkg.CountRunningVMs(result.Value.VMList)
			load.Leases = leases[result.Host]
			counts[result.Host] = load.Count + load.Leases
			reachable = append(reachable, active[i])
			if result.Value.HasImage {
				candidates = append(candidates, active[i])
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
	opts, err := extractFleetRunOptions(args)
	return opts.Policy, opts.RunArgs, err
}

func extractFleetRunOptions(args []string) (fleetRunOptions, error) {
	var opts fleetRunOptions
	opts.RunArgs = make([]string, 0, len(args))
	var policy string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--all" || arg == "-all":
			opts.All = true
		case arg == "--policy" || arg == "-policy":
			if i+1 >= len(args) {
				return fleetRunOptions{}, errors.New("fleet run: --policy requires a value")
			}
			policy = args[i+1]
			i++
		case strings.HasPrefix(arg, "--policy="):
			policy = strings.TrimPrefix(arg, "--policy=")
		case strings.HasPrefix(arg, "-policy="):
			policy = strings.TrimPrefix(arg, "-policy=")
		default:
			opts.RunArgs = append(opts.RunArgs, arg)
		}
	}
	opts.Policy = policy
	return opts, nil
}

func writeFleetRunLines(w io.Writer, host, stream, text string) {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return
	}
	for _, line := range strings.Split(text, "\n") {
		if stream == "" {
			fmt.Fprintf(w, "%s\t%s\n", host, line)
			continue
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", host, stream, line)
	}
}
