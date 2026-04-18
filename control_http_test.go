package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// stubControlServer returns a ControlServer configured with a dummy vmName
// but no real VM, suitable for routes that don't dispatch to a live VM.
func stubControlServer(t *testing.T) *ControlServer {
	t.Helper()
	return &ControlServer{
		authToken: "test-token",
	}
}

func newTestHandler(t *testing.T, ops *OperationRegistry) (http.Handler, *ControlServer) {
	t.Helper()
	cs := stubControlServer(t)
	h := NewHTTPHandler(cs, "test", "test-token", ops)
	return h, cs
}

func TestHTTPHandlerHealthz(t *testing.T) {
	h, _ := newTestHandler(t, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/healthz", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" {
		t.Fatalf("want status=ok, got %q", body["status"])
	}
}

func TestHTTPHandlerHealthzNoAuth(t *testing.T) {
	h, _ := newTestHandler(t, nil)

	// /healthz must work without Authorization header.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/healthz", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 without auth, got %d", rec.Code)
	}
}

func TestHTTPAuthMissing(t *testing.T) {
	h, _ := newTestHandler(t, nil)

	routes := []struct {
		method string
		path   string
	}{
		{"GET", "/v1/vms/test/status"},
		{"POST", "/v1/vms/test/pause"},
		{"POST", "/v1/vms/test/resume"},
		{"POST", "/v1/vms/test/stop"},
		{"GET", "/v1/vms/test/screenshot"},
		{"POST", "/v1/vms/test/type"},
		{"POST", "/v1/vms/test/key"},
		{"POST", "/v1/vms/test/mouse"},
	}
	for _, rt := range routes {
		t.Run(rt.method+" "+rt.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(rt.method, rt.path, nil)
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("want 401, got %d", rec.Code)
			}
			var body map[string]string
			json.Unmarshal(rec.Body.Bytes(), &body) //nolint
			if body["error"] != "unauthorized" {
				t.Fatalf("want error=unauthorized, got %q", body["error"])
			}
		})
	}
}

func TestHTTPAuthWrongToken(t *testing.T) {
	h, _ := newTestHandler(t, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/vms/test/status", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestHTTPPathMismatch(t *testing.T) {
	h, _ := newTestHandler(t, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/vms/other-vm/status", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404 for mismatched vmName, got %d", rec.Code)
	}
}

func TestHTTPOperationsRoundTrip(t *testing.T) {
	store := NewMemOperationStore()
	reg, err := NewOperationRegistry(store)
	if err != nil {
		t.Fatal(err)
	}

	h, _ := newTestHandler(t, reg)

	// Create an operation.
	op, err := reg.Create("vms/test")
	if err != nil {
		t.Fatal(err)
	}

	// GET /v1/operations/<id> — should return the operation.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/operations/"+op.ID, nil)
	req.Header.Set("Authorization", "Bearer test-token")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d; body: %s", rec.Code, rec.Body.String())
	}
	var got Operation
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != op.ID {
		t.Fatalf("want id=%s, got %s", op.ID, got.ID)
	}
	if got.Resource != "vms/test" {
		t.Fatalf("want resource=vms/test, got %s", got.Resource)
	}
	if got.Status != "pending" {
		t.Fatalf("want status=pending, got %s", got.Status)
	}
}

func TestHTTPOperationNotFound(t *testing.T) {
	store := NewMemOperationStore()
	reg, err := NewOperationRegistry(store)
	if err != nil {
		t.Fatal(err)
	}

	h, _ := newTestHandler(t, reg)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/operations/op_nonexistent", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}

func TestHTTPOperationList(t *testing.T) {
	store := NewMemOperationStore()
	reg, err := NewOperationRegistry(store)
	if err != nil {
		t.Fatal(err)
	}

	h, _ := newTestHandler(t, reg)

	// Create two operations.
	reg.Create("vms/test") //nolint
	reg.Create("vms/test") //nolint

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/operations", nil)
	req.Header.Set("Authorization", "Bearer test-token")
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var ops []*Operation
	if err := json.Unmarshal(rec.Body.Bytes(), &ops); err != nil {
		t.Fatal(err)
	}
	if len(ops) != 2 {
		t.Fatalf("want 2 operations, got %d", len(ops))
	}
}

func TestHTTPOperationSSE(t *testing.T) {
	store := NewMemOperationStore()
	reg, err := NewOperationRegistry(store)
	if err != nil {
		t.Fatal(err)
	}

	op, err := reg.Create("vms/test")
	if err != nil {
		t.Fatal(err)
	}

	h, _ := newTestHandler(t, reg)

	// Use a pipe to simulate a streaming response.
	pr, pw := io.Pipe()
	rec := &sseRecorder{w: pw, header: make(http.Header), code: 200}

	done := make(chan struct{})
	var lines []string
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, err := pr.Read(buf)
			if err != nil {
				return
			}
			chunk := string(buf[:n])
			for _, line := range strings.Split(chunk, "\n") {
				if strings.HasPrefix(line, "data: ") {
					lines = append(lines, line)
				}
			}
		}
	}()

	// Start SSE handler in goroutine.
	handlerDone := make(chan struct{})
	go func() {
		defer close(handlerDone)
		req := httptest.NewRequest("GET", "/v1/operations/"+op.ID+"/events", nil)
		req.Header.Set("Authorization", "Bearer test-token")
		h.ServeHTTP(rec, req)
		pw.Close()
	}()

	// Give the SSE handler a moment to subscribe.
	time.Sleep(50 * time.Millisecond)

	// Update to terminal state — this should close the SSE stream.
	if err := reg.Update(op.ID, func(o *Operation) {
		o.Status = "succeeded"
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case <-handlerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("SSE handler did not close on terminal state")
	}
	<-done

	if len(lines) == 0 {
		t.Fatal("expected at least one SSE data line")
	}
	// Last line should contain the op JSON with status=succeeded.
	last := lines[len(lines)-1]
	if !strings.Contains(last, "succeeded") {
		t.Fatalf("expected succeeded in last SSE line, got: %s", last)
	}
}

// sseRecorder implements http.ResponseWriter + http.Flusher backed by an io.PipeWriter.
type sseRecorder struct {
	w      *io.PipeWriter
	header http.Header
	code   int
}

func (r *sseRecorder) Header() http.Header         { return r.header }
func (r *sseRecorder) WriteHeader(code int)         { r.code = code }
func (r *sseRecorder) Flush()                       {}
func (r *sseRecorder) Write(p []byte) (int, error)  { return r.w.Write(p) }
