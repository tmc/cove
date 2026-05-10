package coved

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPrometheusHandler(t *testing.T) {
	h := PrometheusHandler(func() PrometheusSnapshot {
		return PrometheusSnapshot{
			Version:           "v0.5.0",
			UptimeS:           12,
			VMsManaged:        3,
			LifecycleRuns:     2,
			LifecycleErrors:   3,
			EventsDropped:     7,
			ImageGCRuns:       4,
			ImageGCBytes:      99,
			ImageGCDurationMS: 1500,
			ImageGCSkips:      6,
			Events: []Event{{
				EventType: "lifecycle.policy.stop",
				VMName:    "vm1",
				Extra:     map[string]any{"reason": "idle"},
			}},
		}
	})
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`coved_build_info{version="v0.5.0"} 1`,
		"coved_uptime_seconds 12",
		"coved_vms_managed 3",
		"coved_lifecycle_enforced_total 2",
		"coved_lifecycle_stop_errors_total 3",
		"coved_image_gc_runs_total 4",
		"coved_image_gc_bytes_freed_total 99",
		"coved_image_gc_duration_ms_total 1500",
		"coved_image_gc_skips_total 6",
		"coved_eventbus_dropped_total 7",
		`coved_events_total{event_type="lifecycle.policy.stop",vm="vm1",reason="idle"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %q:\n%s", want, body)
		}
	}
}

func TestPrometheusOmitsBuildInfoWhenVersionEmpty(t *testing.T) {
	h := PrometheusHandler(func() PrometheusSnapshot {
		return PrometheusSnapshot{UptimeS: 1}
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if strings.Contains(rec.Body.String(), "coved_build_info") {
		t.Fatalf("should omit coved_build_info:\n%s", rec.Body.String())
	}
}

func TestPrometheusEmitsWebhookCounters(t *testing.T) {
	h := PrometheusHandler(func() PrometheusSnapshot {
		return PrometheusSnapshot{
			WebhookDelivered: 11,
			WebhookFailed:    2,
			WebhookRejected:  5,
		}
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		"coved_webhook_delivered_total 11",
		"coved_webhook_failed_total 2",
		"coved_webhook_rejected_total 5",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %q:\n%s", want, body)
		}
	}
}

func TestPrometheusEmitsStoragePollCounters(t *testing.T) {
	h := PrometheusHandler(func() PrometheusSnapshot {
		return PrometheusSnapshot{
			StoragePollRuns:   17,
			StoragePollErrors: 4,
		}
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		"coved_storage_poll_runs_total 17",
		"coved_storage_poll_errors_total 4",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %q:\n%s", want, body)
		}
	}
}

func TestPrometheusEmitsStorageUsedBytes(t *testing.T) {
	h := PrometheusHandler(func() PrometheusSnapshot {
		return PrometheusSnapshot{StorageUsedBytes: 12345678}
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rec.Body.String(), "coved_storage_used_bytes 12345678") {
		t.Fatalf("metrics missing coved_storage_used_bytes:\n%s", rec.Body.String())
	}
}
