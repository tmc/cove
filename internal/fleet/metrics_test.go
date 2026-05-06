package fleet

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestParsePrometheusMetrics(t *testing.T) {
	body := "# HELP x y\ncoved_vms_managed 2\ncoved_events_total{event_type=\"x\"} 3\ncoved_events_total{event_type=\"y\"} 4\n"
	got := ParsePrometheusMetrics(body)
	if got["coved_vms_managed"] != 2 {
		t.Fatalf("vms = %v, want 2", got["coved_vms_managed"])
	}
	if got["coved_events_total"] != 7 {
		t.Fatalf("events = %v, want 7", got["coved_events_total"])
	}
}

func TestFleetMetricsAggregatesAndKeepsErrors(t *testing.T) {
	entries := []Entry{{Name: "a"}, {Name: "b"}}
	got := FleetMetrics(context.Background(), entries, func(ctx context.Context, entry Entry) (string, error) {
		if entry.Name == "b" {
			return "", errors.New("offline")
		}
		return "coved_vms_managed 2\ncoved_image_gc_runs_total 3\n", nil
	})
	if len(got.Hosts) != 2 {
		t.Fatalf("hosts = %#v", got.Hosts)
	}
	if got.Hosts[1].Error != "offline" {
		t.Fatalf("host b = %#v", got.Hosts[1])
	}
	if got.Totals["coved_vms_managed"] != 2 || got.Totals["coved_image_gc_runs_total"] != 3 {
		t.Fatalf("totals = %#v", got.Totals)
	}
	text := FormatFleetMetrics(got)
	if !strings.Contains(text, "a\tvms=2") || !strings.Contains(text, "b\t(unreachable)") || !strings.Contains(text, "total\tvms=2") {
		t.Fatalf("text = %q", text)
	}
}
