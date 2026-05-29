package fleet

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// HostStatus classifies the result of a fleet health probe.
type HostStatus string

const (
	// StatusOnline means the probe succeeded and returned a usable response.
	StatusOnline HostStatus = "online"
	// StatusDegraded means the host was reachable but the probe response was
	// empty or otherwise inconclusive.
	StatusDegraded HostStatus = "degraded"
	// StatusUnreachable means the probe failed (ssh dial error, timeout, or a
	// non-zero exit on the remote).
	StatusUnreachable HostStatus = "unreachable"
)

// DefaultProbeTimeout bounds each per-host health probe so one slow or hung
// host cannot stall the whole fleet sweep.
const DefaultProbeTimeout = 5 * time.Second

// HostHealth is the outcome of probing a single fleet host.
type HostHealth struct {
	Host       string     `json:"host"`
	Status     HostStatus `json:"status"`
	Detail     string     `json:"detail,omitempty"`
	DurationMS int64      `json:"duration_ms"`
}

// ClassifyProbe maps a probe's raw result to a HostStatus. A non-nil error is
// always unreachable; an empty or whitespace-only output is degraded; anything
// else is online. The returned detail summarizes the reason.
func ClassifyProbe(output string, err error) (HostStatus, string) {
	if err != nil {
		return StatusUnreachable, err.Error()
	}
	if strings.TrimSpace(output) == "" {
		return StatusDegraded, "empty probe response"
	}
	return StatusOnline, ""
}

// ProbeHosts runs probe concurrently against every entry and classifies each
// result into a HostHealth. It is fail-soft: an error from any single host
// becomes an unreachable row rather than aborting the sweep. Results are
// sorted by host name for stable output. A timeout <= 0 falls back to
// DefaultProbeTimeout.
func ProbeHosts(ctx context.Context, entries []Entry, timeout time.Duration, probe QueryFunc[string]) []HostHealth {
	if timeout <= 0 {
		timeout = DefaultProbeTimeout
	}
	results := QueryAllWithTimeout(ctx, entries, timeout, probe)
	health := make([]HostHealth, 0, len(results))
	for _, r := range results {
		status, detail := ClassifyProbe(r.Value, r.Error)
		health = append(health, HostHealth{
			Host:       r.Host,
			Status:     status,
			Detail:     detail,
			DurationMS: r.Duration.Milliseconds(),
		})
	}
	sort.Slice(health, func(i, j int) bool { return health[i].Host < health[j].Host })
	return health
}

// FormatHostHealth renders host health as a tab-separated table, one row per
// host. Hosts with a detail string append it as a third column.
func FormatHostHealth(health []HostHealth) string {
	if len(health) == 0 {
		return "no fleet remotes\n"
	}
	var b strings.Builder
	for _, h := range health {
		if h.Detail != "" {
			fmt.Fprintf(&b, "%s\t%s\t%s\n", h.Host, h.Status, h.Detail)
			continue
		}
		fmt.Fprintf(&b, "%s\t%s\n", h.Host, h.Status)
	}
	return b.String()
}

// HealthSummary counts hosts by status for a quick fleet-wide overview.
type HealthSummary struct {
	Online      int `json:"online"`
	Degraded    int `json:"degraded"`
	Unreachable int `json:"unreachable"`
}

// SummarizeHealth tallies a slice of HostHealth by status.
func SummarizeHealth(health []HostHealth) HealthSummary {
	var s HealthSummary
	for _, h := range health {
		switch h.Status {
		case StatusOnline:
			s.Online++
		case StatusDegraded:
			s.Degraded++
		case StatusUnreachable:
			s.Unreachable++
		}
	}
	return s
}
