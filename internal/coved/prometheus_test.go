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

func TestPrometheusEmitsEventbusSubscribers(t *testing.T) {
	h := PrometheusHandler(func() PrometheusSnapshot {
		return PrometheusSnapshot{EventbusSubs: 3}
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rec.Body.String(), "coved_eventbus_subscribers 3") {
		t.Fatalf("metrics missing coved_eventbus_subscribers:\n%s", rec.Body.String())
	}
}

func TestEventBusSubscribersTracksAttachDetach(t *testing.T) {
	bus := NewEventBus(8)
	if got := bus.Subscribers(); got != 0 {
		t.Fatalf("Subscribers initial = %d, want 0", got)
	}
	_, cancel1 := bus.Subscribe(4)
	_, cancel2 := bus.Subscribe(4)
	if got := bus.Subscribers(); got != 2 {
		t.Fatalf("Subscribers after attach = %d, want 2", got)
	}
	cancel1()
	cancel2()
	if got := bus.Subscribers(); got != 0 {
		t.Fatalf("Subscribers after detach = %d, want 0", got)
	}
}

func TestPrometheusEmitsLifecycleLastRunUnix(t *testing.T) {
	h := PrometheusHandler(func() PrometheusSnapshot {
		return PrometheusSnapshot{LifecycleLastRunUnix: 1715300000}
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rec.Body.String(), "coved_lifecycle_last_run_unix 1715300000") {
		t.Fatalf("metrics missing coved_lifecycle_last_run_unix:\n%s", rec.Body.String())
	}
}

func TestPrometheusEmitsImageGCLastRunUnix(t *testing.T) {
	h := PrometheusHandler(func() PrometheusSnapshot {
		return PrometheusSnapshot{ImageGCLastRunUnix: 1715300555}
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rec.Body.String(), "coved_image_gc_last_run_unix 1715300555") {
		t.Fatalf("metrics missing coved_image_gc_last_run_unix:\n%s", rec.Body.String())
	}
}

func TestPrometheusEmitsStoragePollLastRunUnix(t *testing.T) {
	h := PrometheusHandler(func() PrometheusSnapshot {
		return PrometheusSnapshot{StoragePollLastRunUnix: 1715301234}
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rec.Body.String(), "coved_storage_poll_last_run_unix 1715301234") {
		t.Fatalf("metrics missing coved_storage_poll_last_run_unix:\n%s", rec.Body.String())
	}
}

func TestPrometheusEmitsStorageState(t *testing.T) {
	h := PrometheusHandler(func() PrometheusSnapshot {
		return PrometheusSnapshot{StorageState: "warn"}
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if !strings.Contains(rec.Body.String(), `coved_storage_state{state="warn"} 1`) {
		t.Fatalf("metrics missing coved_storage_state:\n%s", rec.Body.String())
	}
}

func TestPrometheusOmitsStorageStateWhenEmpty(t *testing.T) {
	h := PrometheusHandler(func() PrometheusSnapshot {
		return PrometheusSnapshot{StorageState: ""}
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if strings.Contains(rec.Body.String(), "coved_storage_state") {
		t.Fatalf("expected coved_storage_state to be omitted when empty:\n%s", rec.Body.String())
	}
}

func TestPrometheusEmitsImageGCManifestCounts(t *testing.T) {
	h := PrometheusHandler(func() PrometheusSnapshot {
		return PrometheusSnapshot{
			ImageGCManifestsScanned: 27,
			ImageGCManifestsRemoved: 4,
		}
	})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		"coved_image_gc_manifests_scanned 27",
		"coved_image_gc_manifests_removed 4",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %q:\n%s", want, body)
		}
	}
}
