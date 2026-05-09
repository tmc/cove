package main

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestHTTPPathToControlType(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		path     string
		body     string
		wantType string
		wantBody map[string]any
	}{
		{"status", "GET", "/v1/vms/dev/status", "", "status", nil},
		{"screenshot", "GET", "/v1/vms/dev/screenshot", "", "screenshot", nil},
		{"pause", "POST", "/v1/vms/dev/pause", "", "pause", nil},
		{"resume", "POST", "/v1/vms/dev/resume", "", "resume", nil},
		{"stop with body", "POST", "/v1/vms/dev/stop", `{"force":true}`, "stop", map[string]any{"force": true}},
		{"type", "POST", "/v1/vms/dev/type", `{"text":"hi"}`, "type", map[string]any{"text": "hi"}},
		{"key", "POST", "/v1/vms/dev/key", `{"keycode":36}`, "key", map[string]any{"keycode": float64(36)}},
		{"mouse", "POST", "/v1/vms/dev/mouse", `{"x":10}`, "mouse", map[string]any{"x": float64(10)}},
		{"agent-exec", "POST", "/v1/vms/dev/agent/exec", `{"cmd":"ls"}`, "agent-exec", map[string]any{"cmd": "ls"}},
		{"agent-write", "POST", "/v1/vms/dev/agent/write", `{"path":"/x"}`, "agent-write", map[string]any{"path": "/x"}},
		{"agent-cp", "POST", "/v1/vms/dev/agent/cp", `{"src":"a"}`, "agent-cp", map[string]any{"src": "a"}},
		{"snapshot save", "POST", "/v1/vms/dev/snapshot", `{"name":"cp1"}`, "snapshot", map[string]any{"snapshot": map[string]any{"action": "save", "name": "cp1"}}},
		{"snapshot save no name", "POST", "/v1/vms/dev/snapshot", `{}`, "snapshot", map[string]any{"snapshot": map[string]any{"action": "save"}}},
		{"snapshot list", "GET", "/v1/vms/dev/snapshots", "", "snapshot", map[string]any{"snapshot": map[string]any{"action": "list"}}},
		{"snapshot delete", "DELETE", "/v1/vms/dev/snapshots/cp1", "", "snapshot", map[string]any{"snapshot": map[string]any{"action": "delete", "name": "cp1"}}},
		{"snapshot restore", "POST", "/v1/vms/dev/snapshots/cp1/restore", "", "snapshot", map[string]any{"snapshot": map[string]any{"action": "restore", "name": "cp1"}}},
		{"operations list", "GET", "/v1/vms/dev/operations", "", "operations", map[string]any{"operations": map[string]any{"action": "list"}}},
		{"operations get", "GET", "/v1/vms/dev/operations/op-7", "", "operations", map[string]any{"operations": map[string]any{"action": "get", "id": "op-7"}}},
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
			rec := httptest.NewRecorder()
			gotType, gotBody, err := httpPathToControlType("dev", rec, req)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if gotType != tt.wantType {
				t.Errorf("type = %q, want %q", gotType, tt.wantType)
			}
			if !reflect.DeepEqual(gotBody, tt.wantBody) {
				t.Errorf("body = %#v, want %#v", gotBody, tt.wantBody)
			}
		})
	}
}

func TestHTTPPathToControlTypeAgentRead(t *testing.T) {
	req := httptest.NewRequest("GET", "/v1/vms/dev/agent/read?path=/etc/hosts", nil)
	rec := httptest.NewRecorder()
	gotType, gotBody, err := httpPathToControlType("dev", rec, req)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if gotType != "agent-read" {
		t.Errorf("type = %q, want agent-read", gotType)
	}
	if got, _ := gotBody["path"].(string); got != "/etc/hosts" {
		t.Errorf("path = %q, want /etc/hosts", got)
	}
}

func TestHTTPPathToControlTypeUnhandled(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"unknown path", "GET", "/v1/vms/dev/bogus"},
		{"wrong method", "GET", "/v1/vms/dev/pause"},
		{"snapshot delete with subpath", "DELETE", "/v1/vms/dev/snapshots/cp1/extra"},
		{"snapshot wrong action", "POST", "/v1/vms/dev/snapshots/cp1/bogus"},
		{"operations get with subpath", "GET", "/v1/vms/dev/operations/op/sub"},
		{"operations empty id", "GET", "/v1/vms/dev/operations/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()
			_, _, err := httpPathToControlType("dev", rec, req)
			if err == nil {
				t.Fatal("expected error")
			}
			if rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", rec.Code)
			}
		})
	}
}

func TestSnapshotAndOperationsPayloadHelpers(t *testing.T) {
	if got := snapshotControlPayload("save", ""); !reflect.DeepEqual(got, map[string]any{"snapshot": map[string]any{"action": "save"}}) {
		t.Errorf("save no name = %#v", got)
	}
	if got := snapshotControlPayload("save", "cp"); !reflect.DeepEqual(got, map[string]any{"snapshot": map[string]any{"action": "save", "name": "cp"}}) {
		t.Errorf("save = %#v", got)
	}
	if got := operationsControlPayload("list", ""); !reflect.DeepEqual(got, map[string]any{"operations": map[string]any{"action": "list"}}) {
		t.Errorf("list = %#v", got)
	}
	if got := operationsControlPayload("get", "id-1"); !reflect.DeepEqual(got, map[string]any{"operations": map[string]any{"action": "get", "id": "id-1"}}) {
		t.Errorf("get = %#v", got)
	}
	if got := snapshotNameFromBody(map[string]any{"name": "x"}); got != "x" {
		t.Errorf("name = %q", got)
	}
	if got := snapshotNameFromBody(nil); got != "" {
		t.Errorf("nil body name = %q", got)
	}
	if got := snapshotNameFromBody(map[string]any{"name": 5}); got != "" {
		t.Errorf("non-string name = %q", got)
	}
}
