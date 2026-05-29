// cove fleet control plane (paid layer) — see docs/designs/046-fleet-control-plane.md
package fleet

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fakeScheduler records ScheduleForkRun calls and returns a canned placement, or
// an error when configured. It lets the hosted API run without a worker fleet.
type fakeScheduler struct {
	mu      sync.Mutex
	calls   []ScheduleRequest
	host    string
	err     error
	forkSeq int
}

func (f *fakeScheduler) ScheduleForkRun(req ScheduleRequest, name string) (ForkRunPlacement, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)
	if f.err != nil {
		return ForkRunPlacement{}, f.err
	}
	f.forkSeq++
	host := f.host
	if host == "" {
		host = "host-a"
	}
	return ForkRunPlacement{
		Placement:        Placement{Chosen: host},
		ReservationToken: "res-1",
		ForkAssignmentID: fmt.Sprintf("fork-%d", f.forkSeq),
	}, nil
}

func (f *fakeScheduler) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// fakeCordoner records cordoned hosts.
type fakeCordoner struct {
	mu       sync.Mutex
	cordoned []string
	err      error
}

func (f *fakeCordoner) Cordon(hostID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.cordoned = append(f.cordoned, hostID)
	return nil
}

// hostedHarness wires a HostedAPI behind an httptest server with a controllable
// clock and an API key.
type hostedHarness struct {
	srv     *httptest.Server
	api     *HostedAPI
	sched   *fakeScheduler
	cordon  *fakeCordoner
	ledger  *UsageLedger
	apiKey  string
	nowMu   sync.Mutex
	nowTime time.Time
}

func newHostedHarness(t *testing.T) *hostedHarness {
	t.Helper()
	h := &hostedHarness{
		sched:   &fakeScheduler{},
		cordon:  &fakeCordoner{},
		ledger:  NewUsageLedger(),
		apiKey:  "secret-key",
		nowTime: time.Unix(1_700_000_000, 0),
	}
	now := func() time.Time {
		h.nowMu.Lock()
		defer h.nowMu.Unlock()
		return h.nowTime
	}
	h.ledger.Now = now
	h.api = NewHostedAPI(h.sched, NewSandboxStore(), h.ledger, h.cordon, []string{h.apiKey})
	h.api.Now = now
	h.api.store.Now = now
	h.srv = httptest.NewServer(h.api.Handler())
	t.Cleanup(h.srv.Close)
	return h
}

func (h *hostedHarness) advance(d time.Duration) {
	h.nowMu.Lock()
	defer h.nowMu.Unlock()
	h.nowTime = h.nowTime.Add(d)
}

// req issues an authenticated request and returns the status and decoded body.
func (h *hostedHarness) req(t *testing.T, method, path, key string, body any, hdrs map[string]string) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		enc, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode body: %v", err)
		}
		rdr = bytes.NewReader(enc)
	}
	r, err := http.NewRequest(method, h.srv.URL+path, rdr)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	for k, v := range hdrs {
		r.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func TestHostedAPILifecycle(t *testing.T) {
	h := newHostedHarness(t)

	// Create.
	status, body := h.req(t, http.MethodPost, PathSandboxes, h.apiKey, CreateSandboxRequest{BaseRef: "base:latest", RAMBytes: 1 << 30, VCPUs: 2}, nil)
	if status != http.StatusCreated {
		t.Fatalf("create status = %d, body=%s", status, body)
	}
	var created Sandbox
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("create returned empty id")
	}
	if created.State != SandboxRunning {
		t.Errorf("create state = %q, want running", created.State)
	}
	if created.Host != "" {
		t.Errorf("create leaked host %q; host must be hidden", created.Host)
	}
	if h.sched.callCount() != 1 {
		t.Errorf("scheduler called %d times, want 1", h.sched.callCount())
	}
	id := created.ID

	// Get.
	status, body = h.req(t, http.MethodGet, pathSandbox+id, h.apiKey, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("get status = %d, body=%s", status, body)
	}
	var got Sandbox
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if got.State != SandboxRunning {
		t.Errorf("get state = %q, want running", got.State)
	}

	// Wait returns immediately because it is running.
	status, body = h.req(t, http.MethodPost, pathSandbox+id+"/wait", h.apiKey, WaitRequest{TimeoutMS: 1000}, nil)
	if status != http.StatusOK {
		t.Fatalf("wait status = %d, body=%s", status, body)
	}

	// Metering accrues while running. Advance 30s; usage should reflect it.
	h.advance(30 * time.Second)
	u, ok := h.ledger.Report(id)
	if !ok {
		t.Fatalf("no usage for %s", id)
	}
	if u.WallSeconds != 30 || u.VCPUSeconds != 60 {
		t.Errorf("usage after 30s running = wall %v vcpu %v, want 30/60", u.WallSeconds, u.VCPUSeconds)
	}

	// Stop halts accrual.
	status, _ = h.req(t, http.MethodPost, pathSandbox+id+"/stop", h.apiKey, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("stop status = %d", status)
	}
	h.advance(100 * time.Second) // stopped — must not bill
	u, _ = h.ledger.Report(id)
	if u.WallSeconds != 30 {
		t.Errorf("usage after stop = %v, want 30 (stopped gap must not bill)", u.WallSeconds)
	}
	if u.Running {
		t.Errorf("usage Running = true after stop")
	}

	// Delete.
	status, body = h.req(t, http.MethodDelete, pathSandbox+id, h.apiKey, nil, nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete status = %d, body=%s", status, body)
	}
	status, _ = h.req(t, http.MethodGet, pathSandbox+id, h.apiKey, nil, nil)
	if status != http.StatusOK { // record still readable, state deleted
		t.Fatalf("get-after-delete status = %d", status)
	}
}

func TestHostedAPIAuth(t *testing.T) {
	h := newHostedHarness(t)
	tests := []struct {
		name string
		key  string
		want int
	}{
		{"missing key", "", http.StatusUnauthorized},
		{"bad key", "wrong", http.StatusUnauthorized},
		{"good key", h.apiKey, http.StatusCreated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, _ := h.req(t, http.MethodPost, PathSandboxes, tt.key, CreateSandboxRequest{BaseRef: "base:latest"}, nil)
			if status != tt.want {
				t.Errorf("status = %d, want %d", status, tt.want)
			}
		})
	}
}

func TestHostedAPIOpenWhenNoKeys(t *testing.T) {
	api := NewHostedAPI(&fakeScheduler{}, NewSandboxStore(), NewUsageLedger(), nil, nil)
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(srv.Close)
	body, _ := json.Marshal(CreateSandboxRequest{BaseRef: "base:latest"})
	resp, err := http.Post(srv.URL+PathSandboxes, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("open api status = %d, want 201", resp.StatusCode)
	}
}

func TestHostedAPILeaseBlocksSecondModify(t *testing.T) {
	h := newHostedHarness(t)
	_, body := h.req(t, http.MethodPost, PathSandboxes, h.apiKey, CreateSandboxRequest{BaseRef: "base:latest"}, nil)
	var sb Sandbox
	_ = json.Unmarshal(body, &sb)

	// Acquire a lease.
	status, body := h.req(t, http.MethodPost, pathSandbox+sb.ID+"/lease", h.apiKey, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("lease status = %d, body=%s", status, body)
	}
	var lease LeaseResponse
	if err := json.Unmarshal(body, &lease); err != nil {
		t.Fatalf("decode lease: %v", err)
	}
	if lease.LeaseID == "" {
		t.Fatalf("empty lease id")
	}

	// A second lease must be refused while the first is held.
	status, _ = h.req(t, http.MethodPost, pathSandbox+sb.ID+"/lease", h.apiKey, nil, nil)
	if status != http.StatusConflict {
		t.Errorf("second lease status = %d, want 409", status)
	}

	// A modify without the lease header is blocked.
	status, _ = h.req(t, http.MethodPost, pathSandbox+sb.ID+"/stop", h.apiKey, nil, nil)
	if status != http.StatusConflict {
		t.Errorf("unleased stop status = %d, want 409", status)
	}

	// A modify carrying the lease header succeeds.
	status, _ = h.req(t, http.MethodPost, pathSandbox+sb.ID+"/stop", h.apiKey, nil, map[string]string{LeaseHeader: lease.LeaseID})
	if status != http.StatusOK {
		t.Errorf("leaseholder stop status = %d, want 200", status)
	}
}

func TestHostedAPIWaitTimeout(t *testing.T) {
	h := newHostedHarness(t)
	// Create, then force the sandbox back to pending so wait must time out.
	_, body := h.req(t, http.MethodPost, PathSandboxes, h.apiKey, CreateSandboxRequest{BaseRef: "base:latest"}, nil)
	var sb Sandbox
	_ = json.Unmarshal(body, &sb)
	h.api.store.SetState(sb.ID, SandboxPending, "")

	// Zero timeout: a single non-blocking check that fails because not running.
	status, _ := h.req(t, http.MethodPost, pathSandbox+sb.ID+"/wait", h.apiKey, WaitRequest{TimeoutMS: 0}, nil)
	if status != http.StatusRequestTimeout {
		t.Errorf("wait status = %d, want 408", status)
	}
}

func TestHostedAPICordon(t *testing.T) {
	h := newHostedHarness(t)
	_, body := h.req(t, http.MethodPost, PathSandboxes, h.apiKey, CreateSandboxRequest{BaseRef: "base:latest"}, nil)
	var sb Sandbox
	_ = json.Unmarshal(body, &sb)

	status, body := h.req(t, http.MethodPost, pathSandbox+sb.ID+"/cordon", h.apiKey, nil, nil)
	if status != http.StatusOK {
		t.Fatalf("cordon status = %d, body=%s", status, body)
	}
	h.cordon.mu.Lock()
	defer h.cordon.mu.Unlock()
	if len(h.cordon.cordoned) != 1 || h.cordon.cordoned[0] != "host-a" {
		t.Errorf("cordoned = %v, want [host-a]", h.cordon.cordoned)
	}
}

func TestHostedAPIScheduleUnavailable(t *testing.T) {
	h := newHostedHarness(t)
	h.sched.err = fmt.Errorf("no feasible host")
	status, _ := h.req(t, http.MethodPost, PathSandboxes, h.apiKey, CreateSandboxRequest{BaseRef: "base:latest"}, nil)
	if status != http.StatusServiceUnavailable {
		t.Errorf("create with no host status = %d, want 503", status)
	}
}

func TestHostedAPICreateRejectsMissingBaseRef(t *testing.T) {
	h := newHostedHarness(t)
	status, _ := h.req(t, http.MethodPost, PathSandboxes, h.apiKey, CreateSandboxRequest{}, nil)
	if status != http.StatusBadRequest {
		t.Errorf("create without base_ref status = %d, want 400", status)
	}
}

func TestHostedAPINotFound(t *testing.T) {
	h := newHostedHarness(t)
	status, _ := h.req(t, http.MethodGet, pathSandbox+"sb-nope", h.apiKey, nil, nil)
	if status != http.StatusNotFound {
		t.Errorf("get unknown status = %d, want 404", status)
	}
}
