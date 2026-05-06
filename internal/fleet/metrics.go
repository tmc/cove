package fleet

import (
	"bufio"
	"context"
	"fmt"
	"strconv"
	"strings"
)

type MetricsQueryFunc func(context.Context, Entry) (string, error)

type HostMetrics struct {
	Host    string             `json:"host"`
	Metrics map[string]float64 `json:"metrics,omitempty"`
	Error   string             `json:"error,omitempty"`
}

type FleetMetricsResult struct {
	Hosts  []HostMetrics      `json:"hosts"`
	Totals map[string]float64 `json:"totals"`
}

func FleetMetrics(ctx context.Context, entries []Entry, query MetricsQueryFunc) FleetMetricsResult {
	results := QueryAll(ctx, entries, func(ctx context.Context, entry Entry) (map[string]float64, error) {
		body, err := query(ctx, entry)
		if err != nil {
			return nil, err
		}
		return ParsePrometheusMetrics(body), nil
	})
	out := FleetMetricsResult{Totals: make(map[string]float64)}
	for _, result := range results {
		row := HostMetrics{Host: result.Host, Metrics: result.Value}
		if result.Error != nil {
			row.Error = result.Error.Error()
		} else {
			for name, value := range result.Value {
				out.Totals[name] += value
			}
		}
		out.Hosts = append(out.Hosts, row)
	}
	return out
}

func ParsePrometheusMetrics(body string) map[string]float64 {
	out := make(map[string]float64)
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, rest, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		if i := strings.IndexByte(name, '{'); i >= 0 {
			name = name[:i]
		}
		value, err := strconv.ParseFloat(strings.TrimSpace(rest), 64)
		if err != nil {
			continue
		}
		out[name] += value
	}
	return out
}

func FormatFleetMetrics(result FleetMetricsResult) string {
	var b strings.Builder
	for _, host := range result.Hosts {
		if host.Error != "" {
			fmt.Fprintf(&b, "%s\t(unreachable)\t%s\n", host.Host, host.Error)
			continue
		}
		fmt.Fprintf(&b, "%s\tvms=%g\tgc_runs=%g\tgc_bytes=%g\tlifecycle=%g\n",
			host.Host,
			host.Metrics["coved_vms_managed"],
			host.Metrics["coved_image_gc_runs_total"],
			host.Metrics["coved_image_gc_bytes_freed_total"],
			host.Metrics["coved_lifecycle_enforced_total"])
	}
	fmt.Fprintf(&b, "total\tvms=%g\tgc_runs=%g\tgc_bytes=%g\tlifecycle=%g\n",
		result.Totals["coved_vms_managed"],
		result.Totals["coved_image_gc_runs_total"],
		result.Totals["coved_image_gc_bytes_freed_total"],
		result.Totals["coved_lifecycle_enforced_total"])
	return b.String()
}
