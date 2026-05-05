package softreset

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

const defaultProcessMarker = "cove-softreset-probe"

type Process struct {
	PID     int
	UID     int
	Cmdline string
}

type ProcessTableProbe struct {
	Marker   string
	Snapshot func(context.Context) ([]Process, error)
	Reset    func(context.Context) error
}

func (p ProcessTableProbe) Run(ctx context.Context) (Result, error) {
	snapshot := p.Snapshot
	if snapshot == nil {
		snapshot = ListProcesses
	}
	reset := p.Reset
	if reset == nil {
		reset = func(context.Context) error { return nil }
	}
	marker := p.Marker
	if marker == "" {
		marker = defaultProcessMarker
	}

	before, err := snapshot(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("snapshot before reset: %w", err)
	}
	beforeTagged, beforeSystem := splitProcesses(before, marker)
	evidence := []string{
		fmt.Sprintf("marker=%s", marker),
		fmt.Sprintf("before-total=%d", len(before)),
		fmt.Sprintf("before-cove=%d", len(beforeTagged)),
		fmt.Sprintf("before-system=%d", len(beforeSystem)),
	}
	if len(beforeTagged) == 0 {
		return Result{Probe: "process-table", Status: StatusLimit, Evidence: append(evidence, "cove-spawned=not-observed")}, nil
	}
	if len(beforeSystem) == 0 {
		return Result{Probe: "process-table", Status: StatusLimit, Evidence: append(evidence, "system-processes=not-observed")}, nil
	}

	if err := reset(ctx); err != nil {
		return Result{}, fmt.Errorf("reset: %w", err)
	}

	after, err := snapshot(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("snapshot after reset: %w", err)
	}
	afterTagged, afterSystem := splitProcesses(after, marker)
	survivors := survivingPIDs(beforeSystem, afterSystem)
	evidence = append(evidence,
		fmt.Sprintf("after-total=%d", len(after)),
		fmt.Sprintf("after-cove=%d", len(afterTagged)),
		fmt.Sprintf("after-system=%d", len(afterSystem)),
		fmt.Sprintf("system-survivors=%d", survivors),
	)
	if len(afterTagged) > 0 {
		return Result{Probe: "process-table", Status: StatusFail, Evidence: append(evidence, "cove-spawned=survived")}, nil
	}
	if survivors == 0 {
		return Result{Probe: "process-table", Status: StatusFail, Evidence: append(evidence, "system-processes=no-survivors")}, nil
	}
	return Result{Probe: "process-table", Status: StatusPass, Evidence: append(evidence, "cove-spawned=empty-after-reset")}, nil
}

func splitProcesses(in []Process, marker string) (tagged, system []Process) {
	for _, p := range in {
		if p.PID <= 0 {
			continue
		}
		if strings.Contains(p.Cmdline, marker) {
			tagged = append(tagged, p)
		} else {
			system = append(system, p)
		}
	}
	return tagged, system
}

func survivingPIDs(before, after []Process) int {
	seen := make(map[int]bool, len(after))
	for _, p := range after {
		if p.PID > 0 {
			seen[p.PID] = true
		}
	}
	var n int
	for _, p := range before {
		if seen[p.PID] {
			n++
		}
	}
	return n
}

func parsePID(value string) (int, error) {
	pid, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("invalid pid %q", value)
	}
	return pid, nil
}

func parseUID(value string) (int, error) {
	uid, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || uid < 0 {
		return 0, fmt.Errorf("invalid uid %q", value)
	}
	return uid, nil
}
