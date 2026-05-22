package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

func TestDecodeJSONSuccess(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"x":1}`))
	var got struct{ X int }
	if !decodeJSON(rec, req, &got) {
		t.Fatalf("decodeJSON = false, body = %q", rec.Body.String())
	}
	if got.X != 1 {
		t.Fatalf("X = %d, want 1", got.X)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestDecodeJSONBadInputWrites400(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{`))
	var got struct{}
	if decodeJSON(rec, req, &got) {
		t.Fatal("decodeJSON = true on malformed body, want false")
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "invalid JSON") {
		t.Fatalf("body = %q, want substring 'invalid JSON'", rec.Body.String())
	}
}

func TestWriteJSONEncodesValue(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, map[string]int{"n": 7})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type = %q", got)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"n":7}` {
		t.Fatalf("body = %q, want %q", got, `{"n":7}`)
	}
}

func TestWriteJSONErrorEscapes(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSONError(rec, http.StatusForbidden, `bad "thing"`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"error":"bad \"thing\""}` {
		t.Fatalf("body = %q", got)
	}
}

func TestWriteProtoJSONMarshalsResponse(t *testing.T) {
	rec := httptest.NewRecorder()
	writeProtoJSON(rec, &controlpb.ControlResponse{Success: true, Data: "hi"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type = %q", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"success":true`) || !strings.Contains(body, `"data":"hi"`) {
		t.Fatalf("body = %q", body)
	}
}
