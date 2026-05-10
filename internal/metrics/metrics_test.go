package metrics

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJSONLEmitWritesOneLine(t *testing.T) {
	var buf bytes.Buffer
	sink := NewJSONL(&buf)
	event := Event{
		Timestamp:  "2026-05-05T12:34:56Z",
		EventType:  "vm_start",
		VMName:     "test-vm",
		ImageRef:   "ghcr.io/acme/image:latest",
		DurationMS: 1234,
		Status:     "ok",
		Extra:      map[string]any{"attempt": 2},
	}

	if err := sink.Emit(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(buf.String(), "\n"); got != 1 {
		t.Fatalf("newline count = %d, want 1", got)
	}

	var got Event
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &got); err != nil {
		t.Fatal(err)
	}
	if got.Timestamp != event.Timestamp || got.EventType != event.EventType || got.VMName != event.VMName || got.ImageRef != event.ImageRef || got.DurationMS != event.DurationMS || got.Status != event.Status {
		t.Fatalf("event = %#v, want %#v", got, event)
	}
	if got.Extra["attempt"] != float64(2) {
		t.Fatalf("extra attempt = %#v, want 2", got.Extra["attempt"])
	}
}

func TestJSONLEmitFillsTimestamp(t *testing.T) {
	var buf bytes.Buffer
	before := time.Now().UTC().Add(-time.Second)
	if err := NewJSONL(&buf).Emit(context.Background(), Event{EventType: "vm_stop"}); err != nil {
		t.Fatal(err)
	}
	after := time.Now().UTC().Add(time.Second)

	var got Event
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &got); err != nil {
		t.Fatal(err)
	}
	ts, err := time.Parse(time.RFC3339, got.Timestamp)
	if err != nil {
		t.Fatal(err)
	}
	if ts.Before(before) || ts.After(after) {
		t.Fatalf("timestamp = %s, want between %s and %s", ts, before, after)
	}
}

func TestJSONLEmitRejectsBadTimestamp(t *testing.T) {
	var buf bytes.Buffer
	err := NewJSONL(&buf).Emit(context.Background(), Event{Timestamp: "not a time"})
	if err == nil {
		t.Fatal("Emit returned nil error")
	}
	if !strings.Contains(err.Error(), "invalid timestamp") {
		t.Fatalf("error = %q, want invalid timestamp", err)
	}
}

func TestJSONLEmitChecksContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := NewJSONL(&bytes.Buffer{}).Emit(ctx, Event{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
}

func TestJSONLSinkWritesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics", "events.jsonl")
	sink, err := NewJSONLSink(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.Emit(context.Background(), Event{
		Timestamp: "2026-05-05T12:34:56Z",
		EventType: "vm_start",
	}); err != nil {
		t.Fatal(err)
	}
	if err := sink.Close(); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(b), "\n"); got != 1 {
		t.Fatalf("newline count = %d, want 1", got)
	}
}

func TestNewSinkUsesOTLPFromEnv(t *testing.T) {
	var posted Event
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&posted); err != nil {
			t.Fatalf("decode: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	t.Setenv(otlpEndpointEnv, srv.URL)
	var buf bytes.Buffer

	if err := NewSink(&buf).Emit(context.Background(), Event{Timestamp: "2026-05-05T12:34:56Z", EventType: "run_complete"}); err != nil {
		t.Fatal(err)
	}
	if buf.Len() == 0 {
		t.Fatal("jsonl sink did not write")
	}
	if posted.EventType != "run_complete" {
		t.Fatalf("posted event = %+v", posted)
	}
}

func TestNewSinkDefaultsToJSONL(t *testing.T) {
	t.Setenv(otlpEndpointEnv, "")
	var buf bytes.Buffer

	if err := NewSink(&buf).Emit(context.Background(), Event{Timestamp: "2026-05-05T12:34:56Z"}); err != nil {
		t.Fatal(err)
	}
	if got := buf.String(); !strings.HasSuffix(got, "\n") {
		t.Fatalf("output = %q, want jsonl line", got)
	}
}

func TestOTLPSinkErrorIncludesBodyOnNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("invalid resource attribute schema"))
	}))
	defer srv.Close()

	sink := NewOTLPSink(srv.URL)
	err := sink.Emit(context.Background(), Event{EventType: "x"})
	if err == nil {
		t.Fatal("Emit: want error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid resource attribute schema") {
		t.Fatalf("err = %v, want body excerpt", err)
	}
	if !strings.Contains(err.Error(), "400") {
		t.Fatalf("err = %v, want 400 status", err)
	}
}

func TestOTLPSinkCountsDeliveredAndFailed(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ok.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()

	sink := NewOTLPSink(ok.URL)
	if err := sink.Emit(context.Background(), Event{EventType: "x"}); err != nil {
		t.Fatalf("Emit ok: %v", err)
	}
	if got := sink.Delivered(); got != 1 {
		t.Fatalf("Delivered = %d, want 1", got)
	}

	sink2 := NewOTLPSink(bad.URL)
	if err := sink2.Emit(context.Background(), Event{EventType: "x"}); err == nil {
		t.Fatal("Emit bad: want error, got nil")
	}
	if got := sink2.Failed(); got != 1 {
		t.Fatalf("Failed = %d, want 1", got)
	}
}

func TestOTLPSinkErrorIncludesEventType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	sink := NewOTLPSink(srv.URL)
	err := sink.Emit(context.Background(), Event{EventType: "vm.start"})
	if err == nil {
		t.Fatal("Emit: want error, got nil")
	}
	if !strings.Contains(err.Error(), `event_type="vm.start"`) {
		t.Fatalf("err = %v, want event_type label", err)
	}

	// Connect failure path: closed server, no body.
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv2.Close()
	err = NewOTLPSink(srv2.URL).Emit(context.Background(), Event{EventType: "image.gc"})
	if err == nil {
		t.Fatal("Emit closed server: want error, got nil")
	}
	if !strings.Contains(err.Error(), `event_type="image.gc"`) {
		t.Fatalf("err = %v, want event_type label on transport error", err)
	}
}
