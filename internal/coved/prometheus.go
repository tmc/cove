package coved

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

type PrometheusSnapshot struct {
	Version                 string
	UptimeS                 int64
	VMsManaged              int
	ImageGCRuns             int64
	ImageGCBytes            int64
	ImageGCDurationMS       int64
	ImageGCSkips            int64
	ImageGCLastRunUnix      int64
	ImageGCManifestsScanned int
	ImageGCManifestsRemoved int
	LifecycleRuns           uint64
	LifecycleErrors         uint64
	LifecycleLastRunUnix    int64
	EventsDropped           uint64
	WebhookDelivered        uint64
	WebhookFailed           uint64
	WebhookRejected         uint64
	WebhookLastRunUnix      int64
	StoragePollRuns         int64
	StoragePollErrors       int64
	StoragePollLastRunUnix  int64
	StorageUsedBytes        int64
	StorageState            string
	EventbusSubs            int
	Events                  []Event
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
	fmt.Fprintf(w, "coved_lifecycle_last_run_unix %d\n", s.LifecycleLastRunUnix)
	fmt.Fprintf(w, "coved_image_gc_runs_total %d\n", s.ImageGCRuns)
	fmt.Fprintf(w, "coved_image_gc_bytes_freed_total %d\n", s.ImageGCBytes)
	fmt.Fprintf(w, "coved_image_gc_duration_ms_total %d\n", s.ImageGCDurationMS)
	fmt.Fprintf(w, "coved_image_gc_skips_total %d\n", s.ImageGCSkips)
	fmt.Fprintf(w, "coved_image_gc_last_run_unix %d\n", s.ImageGCLastRunUnix)
	fmt.Fprintf(w, "coved_image_gc_manifests_scanned %d\n", s.ImageGCManifestsScanned)
	fmt.Fprintf(w, "coved_image_gc_manifests_removed %d\n", s.ImageGCManifestsRemoved)
	fmt.Fprintf(w, "coved_eventbus_dropped_total %d\n", s.EventsDropped)
	fmt.Fprintf(w, "coved_webhook_delivered_total %d\n", s.WebhookDelivered)
	fmt.Fprintf(w, "coved_webhook_failed_total %d\n", s.WebhookFailed)
	fmt.Fprintf(w, "coved_webhook_rejected_total %d\n", s.WebhookRejected)
	fmt.Fprintf(w, "coved_webhook_last_run_unix %d\n", s.WebhookLastRunUnix)
	fmt.Fprintf(w, "coved_storage_poll_runs_total %d\n", s.StoragePollRuns)
	fmt.Fprintf(w, "coved_storage_poll_errors_total %d\n", s.StoragePollErrors)
	fmt.Fprintf(w, "coved_storage_poll_last_run_unix %d\n", s.StoragePollLastRunUnix)
	fmt.Fprintf(w, "coved_storage_used_bytes %d\n", s.StorageUsedBytes)
	if s.StorageState != "" {
		fmt.Fprintf(w, "coved_storage_state{state=%q} 1\n", s.StorageState)
	}
	fmt.Fprintf(w, "coved_eventbus_subscribers %d\n", s.EventbusSubs)
	counts := eventCounts(s.Events)
	for _, key := range sortedKeys(counts) {
		labels := strings.Split(key, "\x00")
		fmt.Fprintf(w, "coved_events_total{event_type=%q,vm=%q,reason=%q} %d\n", labels[0], labels[1], labels[2], counts[key])
	}
	captures := captureLatencyStats(s.Events)
	for _, key := range sortedKeys(captures) {
		labels := strings.Split(key, "\x00")
		stat := captures[key]
		fmt.Fprintf(w, "coved_capture_latency_ms_count{backend=%q,requested_backend=%q,fallback=%q,fallback_cause=%q} %d\n", labels[0], labels[1], labels[2], labels[3], stat.count)
		fmt.Fprintf(w, "coved_capture_latency_ms_sum{backend=%q,requested_backend=%q,fallback=%q,fallback_cause=%q} %d\n", labels[0], labels[1], labels[2], labels[3], stat.sum)
		fmt.Fprintf(w, "coved_capture_latency_ms_max{backend=%q,requested_backend=%q,fallback=%q,fallback_cause=%q} %d\n", labels[0], labels[1], labels[2], labels[3], stat.max)
		if stat.errors > 0 {
			fmt.Fprintf(w, "coved_capture_errors_total{backend=%q,requested_backend=%q,fallback_cause=%q} %d\n", labels[0], labels[1], labels[3], stat.errors)
		}
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

type captureLatencyStat struct {
	count  int
	sum    int64
	max    int64
	errors int
}

func captureLatencyStats(events []Event) map[string]captureLatencyStat {
	stats := make(map[string]captureLatencyStat)
	for _, e := range events {
		if e.EventType != "capture_latency" {
			continue
		}
		backend := extraString(e.Extra, "backend")
		requested := extraString(e.Extra, "requested_backend")
		fallback := extraString(e.Extra, "fallback")
		cause := extraString(e.Extra, "fallback_cause")
		key := strings.Join([]string{backend, requested, fallback, cause}, "\x00")
		stat := stats[key]
		stat.count++
		stat.sum += e.DurationMS
		if e.DurationMS > stat.max {
			stat.max = e.DurationMS
		}
		if e.Status == "error" {
			stat.errors++
		}
		stats[key] = stat
	}
	return stats
}

func extraString(extra map[string]any, key string) string {
	if extra == nil {
		return ""
	}
	switch v := extra[key].(type) {
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	default:
		return ""
	}
}

type keyed interface {
	int | captureLatencyStat
}

func sortedKeys[V keyed](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
