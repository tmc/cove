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
			UptimeS:       12,
			VMsManaged:    3,
			LifecycleRuns: 2,
			ImageGCRuns:   4,
			ImageGCBytes:  99,
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
		"coved_uptime_seconds 12",
		"coved_vms_managed 3",
		"coved_lifecycle_enforced_total 2",
		"coved_image_gc_runs_total 4",
		"coved_image_gc_bytes_freed_total 99",
		`coved_events_total{event_type="lifecycle.policy.stop",vm="vm1",reason="idle"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics missing %q:\n%s", want, body)
		}
	}
}
