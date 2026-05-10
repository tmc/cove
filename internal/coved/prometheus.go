package coved

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

type PrometheusSnapshot struct {
	Version           string
	UptimeS           int64
	VMsManaged        int
	ImageGCRuns       int64
	ImageGCBytes      int64
	ImageGCDurationMS int64
	LifecycleRuns     uint64
	LifecycleErrors   uint64
	Events            []Event
}

func PrometheusHandler(snapshot func() PrometheusSnapshot) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metrics" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("content-type", "text/plain; version=0.0.4; charset=utf-8")
		WritePrometheus(w, snapshot())
	})
}

func WritePrometheus(w io.Writer, s PrometheusSnapshot) {
	if s.Version != "" {
		fmt.Fprintf(w, "coved_build_info{version=%q} 1\n", s.Version)
	}
	fmt.Fprintf(w, "coved_uptime_seconds %d\n", s.UptimeS)
	fmt.Fprintf(w, "coved_vms_managed %d\n", s.VMsManaged)
	fmt.Fprintf(w, "coved_lifecycle_enforced_total %d\n", s.LifecycleRuns)
	fmt.Fprintf(w, "coved_lifecycle_stop_errors_total %d\n", s.LifecycleErrors)
	fmt.Fprintf(w, "coved_image_gc_runs_total %d\n", s.ImageGCRuns)
	fmt.Fprintf(w, "coved_image_gc_bytes_freed_total %d\n", s.ImageGCBytes)
	fmt.Fprintf(w, "coved_image_gc_duration_ms_total %d\n", s.ImageGCDurationMS)
	counts := eventCounts(s.Events)
	for _, key := range sortedKeys(counts) {
		labels := strings.Split(key, "\x00")
		fmt.Fprintf(w, "coved_events_total{event_type=%q,vm=%q,reason=%q} %d\n", labels[0], labels[1], labels[2], counts[key])
	}
}

func eventCounts(events []Event) map[string]int {
	counts := make(map[string]int)
	for _, e := range events {
		reason := ""
		if e.Extra != nil {
			if v, ok := e.Extra["reason"].(string); ok {
				reason = v
			}
		}
		key := strings.Join([]string{e.EventType, e.VMName, reason}, "\x00")
		counts[key]++
	}
	return counts
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

