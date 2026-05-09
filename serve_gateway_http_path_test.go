package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPPathToControlType(t *testing.T) {
	tests := []struct {
		name      string
		method    string
		path      string
		body      string
		wantType  string
		wantErr   bool
		wantField string
		wantValue any
	}{
		{name: "status", method: http.MethodGet, path: "/v1/vms/x/status", wantType: "status"},
		{name: "screenshot", method: http.MethodGet, path: "/v1/vms/x/screenshot", wantType: "screenshot"},
		{name: "pause", method: http.MethodPost, path: "/v1/vms/x/pause", wantType: "pause"},
		{name: "resume", method: http.MethodPost, path: "/v1/vms/x/resume", wantType: "resume"},
		{name: "stop", method: http.MethodPost, path: "/v1/vms/x/stop", body: `{"force":true}`, wantType: "stop", wantField: "force", wantValue: true},
		{name: "type", method: http.MethodPost, path: "/v1/vms/x/type", body: `{"text":"hi"}`, wantType: "type", wantField: "text", wantValue: "hi"},
		{name: "key", method: http.MethodPost, path: "/v1/vms/x/key", body: `{}`, wantType: "key"},
		{name: "mouse", method: http.MethodPost, path: "/v1/vms/x/mouse", body: `{}`, wantType: "mouse"},
		{name: "agent-exec", method: http.MethodPost, path: "/v1/vms/x/agent/exec", body: `{}`, wantType: "agent-exec"},
		{name: "agent-read", method: http.MethodGet, path: "/v1/vms/x/agent/read?path=/etc/hosts", wantType: "agent-read", wantField: "path", wantValue: "/etc/hosts"},
		{name: "agent-write", method: http.MethodPost, path: "/v1/vms/x/agent/write", body: `{}`, wantType: "agent-write"},
		{name: "agent-cp", method: http.MethodPost, path: "/v1/vms/x/agent/cp", body: `{}`, wantType: "agent-cp"},
		{name: "snapshot save named", method: http.MethodPost, path: "/v1/vms/x/snapshot", body: `{"name":"snap1"}`, wantType: "snapshot"},
		{name: "snapshot save unnamed", method: http.MethodPost, path: "/v1/vms/x/snapshot", body: `{}`, wantType: "snapshot"},
		{name: "snapshots list", method: http.MethodGet, path: "/v1/vms/x/snapshots", wantType: "snapshot"},
		{name: "operations list", method: http.MethodGet, path: "/v1/vms/x/operations", wantType: "operations"},
		{name: "operations get", method: http.MethodGet, path: "/v1/vms/x/operations/abc", wantType: "operations"},
		{name: "operations get bad slash", method: http.MethodGet, path: "/v1/vms/x/operations/a/b", wantErr: true},
		{name: "operations get empty", method: http.MethodGet, path: "/v1/vms/x/operations/", wantErr: true},
		{name: "snapshot delete", method: http.MethodDelete, path: "/v1/vms/x/snapshots/snap1", wantType: "snapshot"},
		{name: "snapshot restore", method: http.MethodPost, path: "/v1/vms/x/snapshots/snap1/restore", wantType: "snapshot"},
		{name: "snapshot bad parts", method: http.MethodPost, path: "/v1/vms/x/snapshots/a/b/c", wantErr: true},
		{name: "unknown route", method: http.MethodGet, path: "/v1/vms/x/nope", wantErr: true},
		{name: "wrong method", method: http.MethodPost, path: "/v1/vms/x/status", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body *strings.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			}
			var req *http.Request
			if body != nil {
				req = httptest.NewRequest(tt.method, tt.path, body)
			} else {
				req = httptest.NewRequest(tt.method, tt.path, nil)
			}
			w := httptest.NewRecorder()
			gotType, payload, err := httpPathToControlType("x", w, req)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got type %q", gotType)
				}
				if w.Code != http.StatusNotFound {
					t.Errorf("want 404, got %d", w.Code)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotType != tt.wantType {
				t.Errorf("type = %q, want %q", gotType, tt.wantType)
			}
			if tt.wantField != "" {
				if payload[tt.wantField] != tt.wantValue {
					t.Errorf("payload[%q] = %v, want %v", tt.wantField, payload[tt.wantField], tt.wantValue)
				}
			}
		})
	}
}

func TestSnapshotControlPayload(t *testing.T) {
	got := snapshotControlPayload("save", "snap1")
	snap, ok := got["snapshot"].(map[string]any)
	if !ok {
		t.Fatalf("missing snapshot key: %v", got)
	}
	if snap["action"] != "save" || snap["name"] != "snap1" {
		t.Errorf("got %v", snap)
	}
	got = snapshotControlPayload("list", "")
	snap = got["snapshot"].(map[string]any)
	if _, has := snap["name"]; has {
		t.Errorf("empty name should be omitted: %v", snap)
	}
}

func TestSnapshotNameFromBody(t *testing.T) {
	if got := snapshotNameFromBody(map[string]any{"name": "snap1"}); got != "snap1" {
		t.Errorf("got %q", got)
	}
	if got := snapshotNameFromBody(map[string]any{}); got != "" {
		t.Errorf("empty body: got %q", got)
	}
	if got := snapshotNameFromBody(map[string]any{"name": 42}); got != "" {
		t.Errorf("non-string: got %q", got)
	}
}

func TestOperationsControlPayload(t *testing.T) {
	got := operationsControlPayload("get", "abc")
	op := got["operations"].(map[string]any)
	if op["action"] != "get" || op["id"] != "abc" {
		t.Errorf("got %v", op)
	}
	got = operationsControlPayload("list", "")
	op = got["operations"].(map[string]any)
	if _, has := op["id"]; has {
		t.Errorf("empty id should be omitted: %v", op)
	}
}
