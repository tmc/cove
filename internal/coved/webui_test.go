package coved

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebUIHandler(t *testing.T) {
	h := WebUIHandler(func() UISnapshot {
		return UISnapshot{
			Status: map[string]any{"version": "test"},
			Events: []Event{{EventType: "image.gc.run"}},
		}
	}, PrometheusHandler(func() PrometheusSnapshot { return PrometheusSnapshot{UptimeS: 1} }))
	for _, tc := range []struct {
		path string
		want string
	}{
		{path: "/", want: "<title>coved</title>"},
		{path: "/app.js", want: "refresh"},
		{path: "/api/status", want: `"version":"test"`},
		{path: "/api/events", want: `"event_type":"image.gc.run"`},
		{path: "/metrics", want: "coved_uptime_seconds 1"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("code = %d", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Fatalf("body missing %q:\n%s", tc.want, rec.Body.String())
			}
		})
	}
}

func TestWebUIHandlerSnapshotOnce(t *testing.T) {
	var calls int
	h := WebUIHandler(func() UISnapshot {
		calls++
		return UISnapshot{Status: map[string]any{"version": calls}}
	}, PrometheusHandler(func() PrometheusSnapshot { return PrometheusSnapshot{} }))
	for _, path := range []string{"/api/status", "/api/events"} {
		before := calls
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s code = %d", path, rec.Code)
		}
		if calls != before+1 {
			t.Fatalf("%s snapshot calls = %d, want %d", path, calls, before+1)
		}
	}
}
