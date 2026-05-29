// cove fleet control plane (paid layer) — see docs/designs/046-fleet-control-plane.md
package fleet

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tmc/cove/internal/fleet/fleetproto"
)

// Hosted REST API paths. The host is hidden from the caller; sandbox ids are
// opaque. These sit under /v1/sandboxes and are served separately from the
// worker verbs so a deployment can expose them on a public listener while
// keeping worker dial-ins internal.
const (
	PathSandboxes = "/v1/sandboxes"  // POST create, GET list
	pathSandbox   = "/v1/sandboxes/" // GET/DELETE {id}, and {id}/<verb>
)

// Scheduler is the placement surface the hosted API needs. *HostRegistry
// satisfies it via ScheduleForkRun; tests supply a fake. Keeping it an interface
// lets the API run against a fake scheduler without a live worker fleet.
type Scheduler interface {
	ScheduleForkRun(req ScheduleRequest, name string) (ForkRunPlacement, error)
}

// Cordoner marks a host unschedulable. *HostRegistry will satisfy it; the API
// degrades gracefully when nil (cordon returns not-implemented).
type Cordoner interface {
	Cordon(hostID string) error
}

// sandboxRecord is the controller's materialized record for one sandbox. State
// is updated from worker lifecycle reports; lease guards exclusive modification.
type sandboxRecord struct {
	id        string
	state     string
	host      string
	baseRef   string
	jobID     string
	ramBytes  int64
	vcpus     int
	forkID    string
	resToken  string
	createdAt time.Time
	leaseID   string
	leaseExp  time.Time
	cordoned  bool
}

// SandboxStore is the hosted API's sandbox inventory: opaque-id keyed records
// whose state is materialized from worker lifecycle reports. It is safe for
// concurrent use. The zero value is not usable; build one with NewSandboxStore.
type SandboxStore struct {
	// Now is injected for testability; nil falls back to time.Now.
	Now func() time.Time

	mu      sync.Mutex
	records map[string]*sandboxRecord
}

// NewSandboxStore returns an empty store.
func NewSandboxStore() *SandboxStore {
	return &SandboxStore{records: make(map[string]*sandboxRecord)}
}

func (s *SandboxStore) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// newSandboxID returns an opaque, host-hiding sandbox identifier.
func newSandboxID() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate sandbox id: %w", err)
	}
	return "sb-" + hex.EncodeToString(b[:]), nil
}

// put inserts a record. Caller holds s.mu.
func (s *SandboxStore) put(r *sandboxRecord) {
	s.records[r.id] = r
}

// sandbox copies a record's caller-visible handle. The host is deliberately
// omitted: the hosted API hides which Mac runs a sandbox so callers cannot pin
// to hardware (the host is kept internally for cordon and scheduling). Caller
// holds s.mu.
func (r *sandboxRecord) sandbox() Sandbox {
	return Sandbox{ID: r.id, State: r.state}
}

// Get returns the caller-visible handle for a sandbox id.
func (s *SandboxStore) Get(id string) (Sandbox, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.records[id]
	if r == nil {
		return Sandbox{}, false
	}
	return r.sandbox(), true
}

// List returns every sandbox handle sorted by id.
func (s *SandboxStore) List() []Sandbox {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Sandbox, 0, len(s.records))
	for _, r := range s.records {
		out = append(out, r.sandbox())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// SetState transitions a sandbox to state, used when a worker lifecycle report
// (relayed through the controller) materializes the truth. Unknown ids are a
// no-op. The hidden host is recorded the first time it is observed.
func (s *SandboxStore) SetState(id, state, host string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.records[id]
	if r == nil {
		return
	}
	r.state = state
	if host != "" {
		r.host = host
	}
}

// HostedAPI serves the REST /v1/sandboxes surface. Create runs the scheduler and
// enqueues a fork-run assignment; the remaining verbs read or mutate sandbox
// records and feed the metering ledger. Auth is a static set of bearer API keys.
//
// The zero value is not usable; build one with NewHostedAPI. It is safe for
// concurrent use.
type HostedAPI struct {
	scheduler Scheduler
	store     *SandboxStore
	ledger    *UsageLedger
	cordoner  Cordoner

	// DefaultRAMBytes is the memory a create with RAMBytes==0 requests; zero
	// defaults to 2 GiB.
	DefaultRAMBytes int64
	// DefaultVCPUs is the vCPU count metering attributes to a sandbox; zero
	// defaults to 2.
	DefaultVCPUs int
	// LeaseTTL bounds an exclusive-modify lease; zero defaults to 5m.
	LeaseTTL time.Duration
	// Now is injected for testability; nil falls back to time.Now.
	Now func() time.Time

	keys map[string]struct{}
}

// NewHostedAPI builds the API over a scheduler, sandbox store, usage ledger, and
// the set of accepted bearer API keys. An empty keys slice leaves the API open
// (matching the controller's open-when-no-token convention); cordoner may be nil
// to disable cordon. A nil ledger disables metering accrual.
func NewHostedAPI(scheduler Scheduler, store *SandboxStore, ledger *UsageLedger, cordoner Cordoner, keys []string) *HostedAPI {
	km := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		if k != "" {
			km[k] = struct{}{}
		}
	}
	return &HostedAPI{scheduler: scheduler, store: store, ledger: ledger, cordoner: cordoner, keys: km}
}

func (a *HostedAPI) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *HostedAPI) defaultRAM() int64 {
	if a.DefaultRAMBytes > 0 {
		return a.DefaultRAMBytes
	}
	return 2 << 30
}

func (a *HostedAPI) defaultVCPUs() int {
	if a.DefaultVCPUs > 0 {
		return a.DefaultVCPUs
	}
	return 2
}

func (a *HostedAPI) leaseTTL() time.Duration {
	if a.LeaseTTL > 0 {
		return a.LeaseTTL
	}
	return 5 * time.Minute
}

// authorized reports whether the request carries an accepted API key. With no
// keys configured the API is open.
func (a *HostedAPI) authorized(r *http.Request) bool {
	if len(a.keys) == 0 {
		return true
	}
	tok := fleetproto.BearerToken(r)
	if tok == "" {
		return false
	}
	// Constant-time compare against each key so a present-but-wrong key does not
	// leak length via early return.
	ok := false
	for k := range a.keys {
		if subtle.ConstantTimeCompare([]byte(tok), []byte(k)) == 1 {
			ok = true
		}
	}
	return ok
}

// RegisterHandlers wires the hosted sandbox routes onto mux.
func (a *HostedAPI) RegisterHandlers(mux *http.ServeMux) {
	mux.HandleFunc(PathSandboxes, a.handleCollection)
	mux.HandleFunc(pathSandbox, a.handleItem)
}

// Handler returns a standalone mux serving only the hosted routes.
func (a *HostedAPI) Handler() http.Handler {
	mux := http.NewServeMux()
	a.RegisterHandlers(mux)
	return mux
}

// CreateSandboxRequest is the POST /v1/sandboxes body.
type CreateSandboxRequest struct {
	BaseRef  string `json:"base_ref"`
	Name     string `json:"name,omitempty"`
	RAMBytes int64  `json:"ram_bytes,omitempty"`
	VCPUs    int    `json:"vcpus,omitempty"`
	JobID    string `json:"job_id,omitempty"`
	Arch     string `json:"arch,omitempty"`
	MinMacOS string `json:"min_macos,omitempty"`
}

func (a *HostedAPI) handleCollection(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(r) {
		fleetproto.WriteError(w, http.StatusUnauthorized, "api key required")
		return
	}
	switch r.Method {
	case http.MethodPost:
		a.create(w, r)
	case http.MethodGet:
		fleetproto.WriteJSON(w, http.StatusOK, a.store.List())
	default:
		fleetproto.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *HostedAPI) create(w http.ResponseWriter, r *http.Request) {
	req, err := fleetproto.DecodeJSON[CreateSandboxRequest](r)
	if err != nil {
		fleetproto.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.BaseRef == "" {
		fleetproto.WriteError(w, http.StatusBadRequest, "base_ref required")
		return
	}
	ram := req.RAMBytes
	if ram <= 0 {
		ram = a.defaultRAM()
	}
	vcpus := req.VCPUs
	if vcpus <= 0 {
		vcpus = a.defaultVCPUs()
	}
	id, err := newSandboxID()
	if err != nil {
		fleetproto.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	placement, err := a.scheduler.ScheduleForkRun(ScheduleRequest{
		RequestedRAM: ram,
		BaseRef:      req.BaseRef,
		JobID:        req.JobID,
		Arch:         req.Arch,
		MinMacOS:     req.MinMacOS,
	}, req.Name)
	if err != nil {
		fleetproto.WriteError(w, http.StatusServiceUnavailable, fmt.Sprintf("schedule: %v", err))
		return
	}
	rec := &sandboxRecord{
		id:        id,
		state:     SandboxPending,
		host:      placement.Chosen,
		baseRef:   req.BaseRef,
		jobID:     req.JobID,
		ramBytes:  ram,
		vcpus:     vcpus,
		forkID:    placement.ForkAssignmentID,
		resToken:  placement.ReservationToken,
		createdAt: a.now(),
	}
	a.store.mu.Lock()
	a.store.put(rec)
	a.store.mu.Unlock()
	// A fork-run assignment that lands running bills from creation; mark it
	// running and start metering. The worker's eventual report reconciles state.
	a.markRunning(id, placement.Chosen, ram, vcpus)
	fleetproto.WriteJSON(w, http.StatusCreated, Sandbox{ID: id, State: SandboxRunning, Host: ""})
}

// markRunning transitions a sandbox to running and opens a metering interval.
// The host is hidden in the caller-facing handle but recorded internally.
func (a *HostedAPI) markRunning(id, host string, ram int64, vcpus int) {
	a.store.SetState(id, SandboxRunning, host)
	if a.ledger != nil {
		a.ledger.Record(MeterEvent{SandboxID: id, Kind: MeterStart, VCPUs: vcpus, RAMBytes: ram, At: a.now()})
	}
}

// handleItem routes GET/DELETE on {id} and POST on {id}/<verb>.
func (a *HostedAPI) handleItem(w http.ResponseWriter, r *http.Request) {
	if !a.authorized(r) {
		fleetproto.WriteError(w, http.StatusUnauthorized, "api key required")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, pathSandbox)
	if rest == "" {
		fleetproto.WriteError(w, http.StatusNotFound, "sandbox id required")
		return
	}
	id, verb, _ := strings.Cut(rest, "/")
	if id == "" {
		fleetproto.WriteError(w, http.StatusNotFound, "sandbox id required")
		return
	}
	switch verb {
	case "":
		a.itemRoot(w, r, id)
	case "start":
		a.itemTransition(w, r, id, SandboxRunning)
	case "stop":
		a.itemTransition(w, r, id, SandboxStopped)
	case "wait":
		a.itemWait(w, r, id)
	case "lease":
		a.itemLease(w, r, id)
	case "cordon":
		a.itemCordon(w, r, id)
	default:
		fleetproto.WriteError(w, http.StatusNotFound, fmt.Sprintf("unknown verb %q", verb))
	}
}

func (a *HostedAPI) itemRoot(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		sb, ok := a.store.Get(id)
		if !ok {
			fleetproto.WriteError(w, http.StatusNotFound, "sandbox not found")
			return
		}
		fleetproto.WriteJSON(w, http.StatusOK, sb)
	case http.MethodDelete:
		a.delete(w, id)
	default:
		fleetproto.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// requireLeaseOK reports whether a mutating request holds the sandbox lease.
// When no lease is held by anyone, the request is allowed. When a lease is held,
// the request must carry the matching X-Cove-Lease header. Caller holds s.mu.
func (s *SandboxStore) leaseOK(r *sandboxRecord, header string, now time.Time) bool {
	if r.leaseID == "" || now.After(r.leaseExp) {
		return true
	}
	return header != "" && header == r.leaseID
}

// LeaseHeader carries an exclusive-modify lease id on a mutating request.
const LeaseHeader = "X-Cove-Lease"

func (a *HostedAPI) itemTransition(w http.ResponseWriter, r *http.Request, id, target string) {
	if r.Method != http.MethodPost {
		fleetproto.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	a.store.mu.Lock()
	rec := a.store.records[id]
	if rec == nil {
		a.store.mu.Unlock()
		fleetproto.WriteError(w, http.StatusNotFound, "sandbox not found")
		return
	}
	now := a.now()
	if !a.store.leaseOK(rec, r.Header.Get(LeaseHeader), now) {
		a.store.mu.Unlock()
		fleetproto.WriteError(w, http.StatusConflict, "sandbox leased")
		return
	}
	if rec.state == SandboxDeleted {
		a.store.mu.Unlock()
		fleetproto.WriteError(w, http.StatusConflict, "sandbox deleted")
		return
	}
	ram, vcpus := rec.ramBytes, rec.vcpus
	rec.state = target
	sb := rec.sandbox()
	a.store.mu.Unlock()

	if a.ledger != nil {
		switch target {
		case SandboxRunning:
			a.ledger.Record(MeterEvent{SandboxID: id, Kind: MeterStart, VCPUs: vcpus, RAMBytes: ram, At: now})
		case SandboxStopped:
			a.ledger.Record(MeterEvent{SandboxID: id, Kind: MeterStop, At: now})
		}
	}
	fleetproto.WriteJSON(w, http.StatusOK, sb)
}

// WaitRequest is the POST /v1/sandboxes/{id}/wait body. TimeoutMS bounds the
// block; zero means a single non-blocking check.
type WaitRequest struct {
	TimeoutMS int `json:"timeout_ms,omitempty"`
}

func (a *HostedAPI) itemWait(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		fleetproto.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	req, err := fleetproto.DecodeJSON[WaitRequest](r)
	if err != nil {
		// An empty body is a valid zero-timeout wait.
		req = WaitRequest{}
	}
	deadline := a.now().Add(time.Duration(req.TimeoutMS) * time.Millisecond)
	for {
		sb, ok := a.store.Get(id)
		if !ok {
			fleetproto.WriteError(w, http.StatusNotFound, "sandbox not found")
			return
		}
		if sb.State == SandboxRunning {
			fleetproto.WriteJSON(w, http.StatusOK, sb)
			return
		}
		if sb.State == SandboxFailed || sb.State == SandboxDeleted {
			fleetproto.WriteError(w, http.StatusConflict, fmt.Sprintf("sandbox %s", sb.State))
			return
		}
		if !a.now().Before(deadline) {
			fleetproto.WriteError(w, http.StatusRequestTimeout, "wait timed out")
			return
		}
		select {
		case <-r.Context().Done():
			fleetproto.WriteError(w, http.StatusRequestTimeout, "client gone")
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (a *HostedAPI) delete(w http.ResponseWriter, id string) {
	a.store.mu.Lock()
	rec := a.store.records[id]
	if rec == nil {
		a.store.mu.Unlock()
		fleetproto.WriteError(w, http.StatusNotFound, "sandbox not found")
		return
	}
	rec.state = SandboxDeleted
	a.store.mu.Unlock()

	if a.ledger != nil {
		a.ledger.Record(MeterEvent{SandboxID: id, Kind: MeterDelete, At: a.now()})
	}
	w.WriteHeader(http.StatusNoContent)
}

// LeaseResponse acknowledges an exclusive-modify lease.
type LeaseResponse struct {
	LeaseID   string `json:"lease_id"`
	ExpiresAt int64  `json:"expires_at"`
}

func (a *HostedAPI) itemLease(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		fleetproto.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	a.store.mu.Lock()
	defer a.store.mu.Unlock()
	rec := a.store.records[id]
	if rec == nil {
		fleetproto.WriteError(w, http.StatusNotFound, "sandbox not found")
		return
	}
	now := a.now()
	if rec.leaseID != "" && now.Before(rec.leaseExp) {
		fleetproto.WriteError(w, http.StatusConflict, "sandbox already leased")
		return
	}
	lease, err := newLeaseID()
	if err != nil {
		fleetproto.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rec.leaseID = lease
	rec.leaseExp = now.Add(a.leaseTTL())
	fleetproto.WriteJSON(w, http.StatusOK, LeaseResponse{LeaseID: lease, ExpiresAt: rec.leaseExp.Unix()})
}

func (a *HostedAPI) itemCordon(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		fleetproto.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	a.store.mu.Lock()
	rec := a.store.records[id]
	if rec == nil {
		a.store.mu.Unlock()
		fleetproto.WriteError(w, http.StatusNotFound, "sandbox not found")
		return
	}
	rec.cordoned = true
	host := rec.host
	a.store.mu.Unlock()

	if a.cordoner == nil {
		fleetproto.WriteError(w, http.StatusNotImplemented, "cordon not supported")
		return
	}
	if err := a.cordoner.Cordon(host); err != nil {
		fleetproto.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	fleetproto.WriteJSON(w, http.StatusOK, map[string]string{"cordoned": host})
}

// MarkSandboxState lets the controller relay a worker lifecycle report into the
// store and the ledger. A running report opens a metering interval; a
// stopped/failed/deleted report closes it. It is the bridge from worker-reported
// truth to the hosted materialized view.
func (a *HostedAPI) MarkSandboxState(ctx context.Context, id, state, host string) {
	a.store.mu.Lock()
	rec := a.store.records[id]
	if rec == nil {
		a.store.mu.Unlock()
		return
	}
	rec.state = state
	if host != "" {
		rec.host = host
	}
	ram, vcpus := rec.ramBytes, rec.vcpus
	a.store.mu.Unlock()

	if a.ledger == nil {
		return
	}
	now := a.now()
	switch state {
	case SandboxRunning:
		a.ledger.Record(MeterEvent{SandboxID: id, Kind: MeterStart, VCPUs: vcpus, RAMBytes: ram, At: now})
	case SandboxStopped:
		a.ledger.Record(MeterEvent{SandboxID: id, Kind: MeterStop, At: now})
	case SandboxFailed, SandboxDeleted:
		a.ledger.Record(MeterEvent{SandboxID: id, Kind: MeterDelete, At: now})
	}
}
