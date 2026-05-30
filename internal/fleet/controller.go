// cove fleet control plane (paid layer) — see docs/designs/046-fleet-control-plane.md
package fleet

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/tmc/cove/internal/fleet/fleetproto"
)

// DefaultHostOnlineWindow is how recently a host must have heartbeated to count
// as online.
const DefaultHostOnlineWindow = 90 * time.Second

// Host is the controller's record for one registered worker. Live VM facts
// (FreeRAMBytes, VMCount, Images, RunningVMs) are materialized from the most
// recent heartbeat; the controller keeps no authoritative global VM ledger.
type Host struct {
	HostID       string   `json:"host_id"`
	Hostname     string   `json:"hostname"`
	Arch         string   `json:"arch"`
	MacOSVersion string   `json:"macos_version"`
	LeaseID      string   `json:"lease_id"`
	RegisteredAt int64    `json:"registered_at"`
	LastSeenUnix int64    `json:"last_seen_unix"`
	FreeRAMBytes int64    `json:"free_ram_bytes"`
	VMCount      int      `json:"vm_count"`
	Images       []string `json:"images,omitempty"`
	RunningVMs   []string `json:"running_vms,omitempty"`
}

// controllerState is the persisted shape of the embedded store.
type controllerState struct {
	Hosts map[string]*Host `json:"hosts"`
}

// HostRegistry is the controller's embedded host-inventory store: a
// mutex-guarded JSON file persisted atomically via tmp+rename. It is a single
// point of failure by design (back up the state file); it holds fleet
// configuration, not a consensus log.
type HostRegistry struct {
	// Now is injected for testability; nil falls back to time.Now.
	Now func() time.Time
	// OnlineWindow overrides DefaultHostOnlineWindow when non-zero.
	OnlineWindow time.Duration

	mu       sync.Mutex
	path     string
	hosts    map[string]*Host
	queue    map[string][]fleetproto.Assignment
	results  map[string]*pushRecord
	seq      uint64
	regToken string

	// reservations holds RAM committed by Schedule but not yet reflected in a
	// host heartbeat. Guarded by mu; see scheduler.go. resSeq mints tokens.
	reservations map[string]reservation
	resSeq       uint64

	// cordoned holds hosts an operator marked unschedulable. Guarded by mu; see
	// cordon.go. The scheduler's feasibility filter skips cordoned hosts.
	cordoned map[string]struct{}
}

// NewHostRegistry opens (or creates) the registry backed by statePath. The
// register token guards the Register verb; subsequent calls authenticate with
// the per-host lease.
func NewHostRegistry(statePath, registerToken string) (*HostRegistry, error) {
	r := &HostRegistry{
		path:     statePath,
		hosts:    make(map[string]*Host),
		queue:    make(map[string][]fleetproto.Assignment),
		regToken: registerToken,
	}
	if statePath != "" {
		if err := r.load(); err != nil {
			return nil, err
		}
	}
	return r, nil
}

func (r *HostRegistry) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *HostRegistry) onlineWindow() time.Duration {
	if r.OnlineWindow > 0 {
		return r.OnlineWindow
	}
	return DefaultHostOnlineWindow
}

func (r *HostRegistry) load() error {
	data, err := os.ReadFile(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read fleet state: %w", err)
	}
	var st controllerState
	if err := json.Unmarshal(data, &st); err != nil {
		return fmt.Errorf("parse fleet state: %w", err)
	}
	if st.Hosts != nil {
		r.hosts = st.Hosts
	}
	return nil
}

// persist writes the host map atomically. The caller must hold r.mu. The
// assignment queue is intentionally in-memory only: queued work is best-effort
// and re-derived from policy after a controller restart.
func (r *HostRegistry) persist() error {
	if r.path == "" {
		return nil
	}
	st := controllerState{Hosts: r.hosts}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal fleet state: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0700); err != nil {
		return fmt.Errorf("create fleet state dir: %w", err)
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0600); err != nil {
		return fmt.Errorf("write fleet state: %w", err)
	}
	if err := os.Rename(tmp, r.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename fleet state: %w", err)
	}
	return nil
}

// Register records a host and issues a fresh lease. The supplied token must
// match the registry's register token when one is configured.
func (r *HostRegistry) Register(req fleetproto.Register) (fleetproto.RegisterResp, error) {
	if req.HostID == "" {
		return fleetproto.RegisterResp{}, fmt.Errorf("register: host id required")
	}
	if r.regToken != "" && req.Token != r.regToken {
		return fleetproto.RegisterResp{}, fmt.Errorf("register: invalid token")
	}
	lease, err := newLeaseID()
	if err != nil {
		return fleetproto.RegisterResp{}, err
	}
	now := r.now().Unix()
	r.mu.Lock()
	defer r.mu.Unlock()
	h := r.hosts[req.HostID]
	if h == nil {
		h = &Host{HostID: req.HostID, RegisteredAt: now}
		r.hosts[req.HostID] = h
	}
	h.Hostname = req.Hostname
	h.Arch = req.Arch
	h.MacOSVersion = req.MacOSVersion
	h.LeaseID = lease
	h.LastSeenUnix = now
	if err := r.persist(); err != nil {
		return fleetproto.RegisterResp{}, err
	}
	return fleetproto.RegisterResp{HostID: req.HostID, LeaseID: lease, OK: true}, nil
}

// Heartbeat updates last-seen and live host facts, then returns any queued
// assignments for the host, clearing the queue.
func (r *HostRegistry) Heartbeat(req fleetproto.Heartbeat) (fleetproto.HeartbeatResp, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	h := r.hosts[req.HostID]
	if h == nil {
		return fleetproto.HeartbeatResp{}, fmt.Errorf("heartbeat: host %q not registered", req.HostID)
	}
	if h.LeaseID != req.LeaseID {
		return fleetproto.HeartbeatResp{}, fmt.Errorf("heartbeat: invalid lease")
	}
	h.LastSeenUnix = r.now().Unix()
	h.FreeRAMBytes = req.FreeRAMBytes
	h.VMCount = req.VMCount
	h.Images = append([]string(nil), req.Images...)
	h.RunningVMs = append([]string(nil), req.RunningVMs...)
	if err := r.persist(); err != nil {
		return fleetproto.HeartbeatResp{}, err
	}
	assigned := r.queue[req.HostID]
	delete(r.queue, req.HostID)
	return fleetproto.HeartbeatResp{Assignments: assigned}, nil
}

// Assignments returns and clears the queued assignments for a host after
// validating its lease. The live worker protocol delivers assignments on the
// heartbeat response (see Heartbeat); this method exposes the same lease-checked
// queue drain for operator tooling and tests that need to inspect what was
// enqueued without a full heartbeat round-trip.
func (r *HostRegistry) Assignments(hostID, leaseID string) ([]fleetproto.Assignment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	h := r.hosts[hostID]
	if h == nil {
		return nil, fmt.Errorf("assignments: host %q not registered", hostID)
	}
	if h.LeaseID != leaseID {
		return nil, fmt.Errorf("assignments: invalid lease")
	}
	assigned := r.queue[hostID]
	delete(r.queue, hostID)
	return assigned, nil
}

// ReportStatus validates the lease and records the report. When the report
// matches an assignment enqueued by a policy or image-gc push, the worker's
// terminal state and structured detail are stored so AggregateResults can build
// a per-host outcome map.
func (r *HostRegistry) ReportStatus(req fleetproto.ReportStatus) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	h := r.hosts[req.HostID]
	if h == nil {
		return fmt.Errorf("report: host %q not registered", req.HostID)
	}
	if h.LeaseID != req.LeaseID {
		return fmt.Errorf("report: invalid lease")
	}
	if rec := r.results[req.AssignmentID]; rec != nil && rec.hostID == req.HostID {
		rec.state = req.State
		rec.detail = req.Detail
	}
	return nil
}

// Enqueue queues an assignment for a host, assigning it a unique ID when blank.
// It is the controller-side entry point for scheduler/policy code (Slices 6-7).
func (r *HostRegistry) Enqueue(hostID string, a fleetproto.Assignment) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.hosts[hostID]; !ok {
		return "", fmt.Errorf("enqueue: host %q not registered", hostID)
	}
	if a.ID == "" {
		r.seq++
		a.ID = fmt.Sprintf("a-%d", r.seq)
	}
	r.queue[hostID] = append(r.queue[hostID], a)
	return a.ID, nil
}

// Online reports whether the host heartbeated within the online window.
func (r *HostRegistry) Online(hostID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	h := r.hosts[hostID]
	if h == nil {
		return false
	}
	return r.isOnline(h)
}

func (r *HostRegistry) isOnline(h *Host) bool {
	if h.LastSeenUnix == 0 {
		return false
	}
	last := time.Unix(h.LastSeenUnix, 0)
	return r.now().Sub(last) <= r.onlineWindow()
}

// List returns a copy of all registered hosts sorted by host ID.
func (r *HostRegistry) List() []Host {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Host, 0, len(r.hosts))
	for _, h := range r.hosts {
		out = append(out, *h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].HostID < out[j].HostID })
	return out
}

// newLeaseID returns a cryptographically random opaque lease identifier.
func newLeaseID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate lease id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// Controller serves the worker protocol over HTTP backed by a HostRegistry.
type Controller struct {
	Registry *HostRegistry
}

// NewController builds a Controller over the given registry.
func NewController(reg *HostRegistry) *Controller {
	return &Controller{Registry: reg}
}

// Handler returns the HTTP handler implementing the worker verbs (register,
// heartbeat, report-status; the heartbeat response delivers queued assignments)
// plus the operator-facing policy/image-gc push and results endpoints.
func (c *Controller) Handler() http.Handler {
	mux := http.NewServeMux()
	c.RegisterHandlers(mux)
	return mux
}

// RegisterHandlers adds the worker verbs and operator-facing endpoints to mux.
// It is exposed so a deployment can mount the hosted /v1/sandboxes API on the
// same mux (see cmd/cove-fleetd).
func (c *Controller) RegisterHandlers(mux *http.ServeMux) {
	c.RegisterWorkerHandlers(mux)
	c.RegisterOperatorHandlers(mux)
}

// RegisterWorkerHandlers adds only the worker verbs (register, heartbeat,
// report-status) to mux. A deployment that wraps the operator endpoints in
// RBAC/audit middleware (Slice 9) registers the worker verbs here and mounts
// the operator surface separately so the worker dial-ins, authenticated by the
// per-host lease, stay unwrapped.
func (c *Controller) RegisterWorkerHandlers(mux *http.ServeMux) {
	mux.HandleFunc(fleetproto.PathRegister, c.handleRegister)
	mux.HandleFunc(fleetproto.PathHeartbeat, c.handleHeartbeat)
	mux.HandleFunc(fleetproto.PathStatus, c.handleStatus)
}

func (c *Controller) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fleetproto.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	req, err := fleetproto.DecodeJSON[fleetproto.Register](r)
	if err != nil {
		fleetproto.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	resp, err := c.Registry.Register(req)
	if err != nil {
		fleetproto.WriteError(w, http.StatusUnauthorized, err.Error())
		return
	}
	fleetproto.WriteJSON(w, http.StatusOK, resp)
}

func (c *Controller) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fleetproto.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	req, err := fleetproto.DecodeJSON[fleetproto.Heartbeat](r)
	if err != nil {
		fleetproto.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if fleetproto.BearerToken(r) != req.LeaseID {
		fleetproto.WriteError(w, http.StatusUnauthorized, "lease mismatch")
		return
	}
	resp, err := c.Registry.Heartbeat(req)
	if err != nil {
		fleetproto.WriteError(w, http.StatusUnauthorized, err.Error())
		return
	}
	fleetproto.WriteJSON(w, http.StatusOK, resp)
}

func (c *Controller) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fleetproto.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	req, err := fleetproto.DecodeJSON[fleetproto.ReportStatus](r)
	if err != nil {
		fleetproto.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	if fleetproto.BearerToken(r) != req.LeaseID {
		fleetproto.WriteError(w, http.StatusUnauthorized, "lease mismatch")
		return
	}
	if err := c.Registry.ReportStatus(req); err != nil {
		fleetproto.WriteError(w, http.StatusUnauthorized, err.Error())
		return
	}
	fleetproto.WriteJSON(w, http.StatusOK, fleetproto.StatusAck{OK: true})
}
