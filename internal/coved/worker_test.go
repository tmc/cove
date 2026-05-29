package coved_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/tmc/cove/internal/coved"
	"github.com/tmc/cove/internal/fleet"
	"github.com/tmc/cove/internal/fleet/fleetproto"
)

// newControllerServer builds a real controller over httptest so worker tests
// exercise the full wire protocol. It returns the registry and the server URL.
func newControllerServer(t *testing.T) (*fleet.HostRegistry, string) {
	t.Helper()
	reg, err := fleet.NewHostRegistry("", "tok")
	if err != nil {
		t.Fatalf("NewHostRegistry: %v", err)
	}
	srv := newHTTPTestServer(t, fleet.NewController(reg).Handler())
	return reg, srv
}

func newHTTPTestServer(t *testing.T, h http.Handler) string {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestWorkerRegisterAndHeartbeatCarriesFacts(t *testing.T) {
	reg, url := newControllerServer(t)
	facts := coved.HostFacts{FreeRAMBytes: 8 << 30, VMCount: 3, Images: []string{"base:latest"}, RunningVMs: []string{"runner-1"}}
	w, err := coved.NewWorker(coved.WorkerConfig{
		ControllerURL: url,
		Token:         "tok",
		HostID:        "h1",
		Hostname:      "mac1",
		Facts:         func() coved.HostFacts { return facts },
		Handler:       &coved.BoundedHandler{},
	})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	ctx := context.Background()
	if err := w.Register(ctx); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if w.LeaseID() == "" {
		t.Fatal("lease not set after register")
	}
	if _, err := w.Heartbeat(ctx); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	hosts := reg.List()
	if len(hosts) != 1 {
		t.Fatalf("hosts = %d, want 1", len(hosts))
	}
	h := hosts[0]
	if h.FreeRAMBytes != facts.FreeRAMBytes || h.VMCount != facts.VMCount {
		t.Fatalf("facts not carried: %+v", h)
	}
	if len(h.Images) != 1 || h.Images[0] != "base:latest" {
		t.Fatalf("images = %v, want [base:latest]", h.Images)
	}
	if len(h.RunningVMs) != 1 || h.RunningVMs[0] != "runner-1" {
		t.Fatalf("running vms = %v, want [runner-1]", h.RunningVMs)
	}
}

// recordingHandler captures handled assignments and answers with a fixed state.
type recordingHandler struct {
	mu       sync.Mutex
	handled  []fleetproto.Assignment
	retState string
}

func (h *recordingHandler) Handle(ctx context.Context, a fleetproto.Assignment) (string, string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.handled = append(h.handled, a)
	return h.retState, "ok", nil
}

func (h *recordingHandler) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.handled)
}

func TestWorkerRefusesHostShellAssignment(t *testing.T) {
	reg, url := newControllerServer(t)
	handler := &recordingHandler{retState: fleetproto.StateDone}
	w, err := coved.NewWorker(coved.WorkerConfig{
		ControllerURL: url,
		Token:         "tok",
		HostID:        "h1",
		Facts:         func() coved.HostFacts { return coved.HostFacts{} },
		Handler:       handler,
	})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	ctx := context.Background()
	if err := w.Register(ctx); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// Queue a host-shell assignment the worker must refuse before dispatch.
	if _, err := reg.Enqueue("h1", fleetproto.Assignment{Kind: "host-shell"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	n, err := w.Heartbeat(ctx)
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if n != 1 {
		t.Fatalf("handled %d assignments, want 1 (refusal still counts as delivered)", n)
	}
	if handler.count() != 0 {
		t.Fatalf("bounded handler was invoked %d times for host-shell, want 0 (refused before dispatch)", handler.count())
	}
}

func TestWorkerDispatchesKnownKind(t *testing.T) {
	reg, url := newControllerServer(t)
	handler := &recordingHandler{retState: fleetproto.StateDone}
	w, err := coved.NewWorker(coved.WorkerConfig{
		ControllerURL: url,
		Token:         "tok",
		HostID:        "h1",
		Facts:         func() coved.HostFacts { return coved.HostFacts{} },
		Handler:       handler,
	})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	ctx := context.Background()
	if err := w.Register(ctx); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, err := reg.Enqueue("h1", fleetproto.Assignment{Kind: fleetproto.KindStopVM}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := w.Heartbeat(ctx); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if handler.count() != 1 {
		t.Fatalf("handler invoked %d times, want 1", handler.count())
	}
}

func TestBoundedHandlerRefusesUnknownKind(t *testing.T) {
	h := &coved.BoundedHandler{}
	state, _, err := h.Handle(context.Background(), fleetproto.Assignment{Kind: "exec"})
	if err == nil {
		t.Fatal("expected unknown kind to error")
	}
	if state != fleetproto.StateRefused {
		t.Fatalf("state = %q, want refused", state)
	}
}

func TestBoundedHandlerRunsHook(t *testing.T) {
	var called bool
	h := &coved.BoundedHandler{
		StopVM: func(ctx context.Context, payload []byte) (string, error) {
			called = true
			return "stopped", nil
		},
	}
	state, detail, err := h.Handle(context.Background(), fleetproto.Assignment{Kind: fleetproto.KindStopVM})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !called {
		t.Fatal("StopVM hook not called")
	}
	if state != fleetproto.StateDone || detail != "stopped" {
		t.Fatalf("state/detail = %q/%q, want done/stopped", state, detail)
	}
}

func TestNewWorkerValidation(t *testing.T) {
	tests := []struct {
		name string
		cfg  coved.WorkerConfig
	}{
		{"missing url", coved.WorkerConfig{Handler: &coved.BoundedHandler{}}},
		{"missing handler", coved.WorkerConfig{ControllerURL: "http://x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := coved.NewWorker(tt.cfg); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestWorkerRunStopsOnContextCancel(t *testing.T) {
	_, url := newControllerServer(t)
	w, err := coved.NewWorker(coved.WorkerConfig{
		ControllerURL: url,
		Token:         "tok",
		HostID:        "h1",
		Interval:      5 * time.Millisecond,
		Facts:         func() coved.HostFacts { return coved.HostFacts{} },
		Handler:       &coved.BoundedHandler{},
	})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	// Let it register and heartbeat a few times, then cancel.
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after context cancel")
	}
}
