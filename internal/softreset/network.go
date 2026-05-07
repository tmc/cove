package softreset

import (
	"context"
	"fmt"
	"strings"
)

const defaultNetworkMarker = "cove-softreset-probe"

type NetworkSocket struct {
	Protocol string
	Local    string
	Remote   string
	State    string
	Process  string
}

type NetworkProbe struct {
	Marker   string
	Snapshot func(context.Context) ([]NetworkSocket, error)
	Reset    func(context.Context) error
}

func (p NetworkProbe) Run(ctx context.Context) (Result, error) {
	snapshot := p.Snapshot
	if snapshot == nil {
		snapshot = ListNetworkSockets
	}
	reset := p.Reset
	if reset == nil {
		reset = func(context.Context) error { return nil }
	}
	marker := p.Marker
	if marker == "" {
		marker = defaultNetworkMarker
	}

	before, err := snapshot(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("snapshot before reset: %w", err)
	}
	beforeTagged, beforeSystem := splitNetworkSockets(before, marker)
	evidence := []string{
		fmt.Sprintf("marker=%s", marker),
		fmt.Sprintf("before-total=%d", len(before)),
		fmt.Sprintf("before-cove=%d", len(beforeTagged)),
		fmt.Sprintf("before-system=%d", len(beforeSystem)),
	}
	if len(beforeTagged) == 0 {
		return Result{Probe: "network", Status: StatusLimit, Evidence: append(evidence, "cove-sockets=not-observed")}, nil
	}
	if len(beforeSystem) == 0 {
		return Result{Probe: "network", Status: StatusLimit, Evidence: append(evidence, "system-sockets=not-observed")}, nil
	}

	if err := reset(ctx); err != nil {
		return Result{}, fmt.Errorf("reset: %w", err)
	}

	after, err := snapshot(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("snapshot after reset: %w", err)
	}
	afterTagged, afterSystem := splitNetworkSockets(after, marker)
	survivors := survivingNetworkSockets(beforeSystem, afterSystem)
	evidence = append(evidence,
		fmt.Sprintf("after-total=%d", len(after)),
		fmt.Sprintf("after-cove=%d", len(afterTagged)),
		fmt.Sprintf("after-system=%d", len(afterSystem)),
		fmt.Sprintf("system-survivors=%d", survivors),
	)
	if len(afterTagged) > 0 {
		return Result{Probe: "network", Status: StatusFail, Evidence: append(evidence, "cove-sockets=survived")}, nil
	}
	if survivors == 0 {
		return Result{Probe: "network", Status: StatusFail, Evidence: append(evidence, "system-sockets=no-survivors")}, nil
	}
	return Result{Probe: "network", Status: StatusPass, Evidence: append(evidence, "cove-sockets=empty-after-reset")}, nil
}

func splitNetworkSockets(in []NetworkSocket, marker string) (tagged, system []NetworkSocket) {
	for _, s := range in {
		if strings.Contains(s.Process, marker) {
			tagged = append(tagged, s)
		} else {
			system = append(system, s)
		}
	}
	return tagged, system
}

func survivingNetworkSockets(before, after []NetworkSocket) int {
	seen := make(map[string]bool, len(after))
	for _, s := range after {
		seen[networkSocketKey(s)] = true
	}
	var n int
	for _, s := range before {
		if seen[networkSocketKey(s)] {
			n++
		}
	}
	return n
}

func networkSocketKey(s NetworkSocket) string {
	return s.Protocol + "|" + s.Local + "|" + s.Remote + "|" + s.State
}

