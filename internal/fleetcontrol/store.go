package fleetcontrol

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Store struct {
	mu            sync.Mutex
	path          string
	ttl           time.Duration
	assignmentTTL time.Duration
	now           func() time.Time
	hosts         map[string]HostRecord
	assignments   map[string]Assignment
	warmPools     map[string]WarmPool
	audit         []AuditEvent
	metering      []SandboxMeteringRecord
	accounts      map[string]serviceAccountRecord
	oidcBindings  map[string]oidcBindingRecord
}

type storeFile struct {
	Hosts           []HostRecord            `json:"hosts"`
	Assignments     []Assignment            `json:"assignments,omitempty"`
	WarmPools       []WarmPool              `json:"warm_pools,omitempty"`
	AuditEvents     []AuditEvent            `json:"audit_events,omitempty"`
	MeteringRecords []SandboxMeteringRecord `json:"metering_records,omitempty"`
	ServiceAccounts []serviceAccountRecord  `json:"service_accounts,omitempty"`
	OIDCBindings    []oidcBindingRecord     `json:"oidc_bindings,omitempty"`
}

type serviceAccountRecord struct {
	Name      string    `json:"name"`
	Namespace string    `json:"namespace,omitempty"`
	Role      string    `json:"role,omitempty"`
	TokenHash string    `json:"token_sha256"`
	Created   time.Time `json:"created,omitempty"`
	Updated   time.Time `json:"updated,omitempty"`
}

type oidcBindingRecord struct {
	Name        string          `json:"name"`
	Issuer      string          `json:"issuer"`
	Subject     string          `json:"subject"`
	Audience    string          `json:"audience"`
	Namespace   string          `json:"namespace,omitempty"`
	Role        string          `json:"role,omitempty"`
	JWKSURL     string          `json:"jwks_url,omitempty"`
	JWKSFetched time.Time       `json:"jwks_fetched,omitempty"`
	Keys        []oidcKeyRecord `json:"keys,omitempty"`
	Created     time.Time       `json:"created,omitempty"`
	Updated     time.Time       `json:"updated,omitempty"`
}

type oidcKeyRecord struct {
	KID string `json:"kid,omitempty"`
	Alg string `json:"alg,omitempty"`
	PEM string `json:"pem"`
}

const (
	sandboxRoleRun     = "run"
	sandboxRoleStop    = "stop"
	sandboxRoleExec    = "exec"
	sandboxRoleControl = "control"
)

func OpenStore(path string, ttl time.Duration) (*Store, error) {
	if ttl <= 0 {
		ttl = DefaultWorkerTTL
	}
	s := &Store{
		path:          strings.TrimSpace(path),
		ttl:           ttl,
		assignmentTTL: DefaultAssignmentTTL,
		now:           time.Now,
		hosts:         make(map[string]HostRecord),
		assignments:   make(map[string]Assignment),
		warmPools:     make(map[string]WarmPool),
		audit:         nil,
		metering:      nil,
		accounts:      make(map[string]serviceAccountRecord),
		oidcBindings:  make(map[string]oidcBindingRecord),
	}
	if s.path == "" {
		return s, nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("read fleet store: %w", err)
	}
	var file storeFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, fmt.Errorf("decode fleet store: %w", err)
	}
	for _, host := range file.Hosts {
		id := strings.TrimSpace(host.ID)
		if id == "" {
			continue
		}
		host.ID = id
		host.ImageRefs = sortedUniqueStrings(host.ImageRefs)
		s.hosts[id] = host
	}
	for _, assignment := range file.Assignments {
		id := strings.TrimSpace(assignment.ID)
		if id == "" {
			continue
		}
		assignment.ID = id
		assignment.Namespace = normalizeNamespace(assignment.Namespace)
		assignment.Args = cloneStrings(assignment.Args)
		assignment.WarmPool = strings.TrimSpace(assignment.WarmPool)
		assignment.WarmPoolSlot = strings.TrimSpace(assignment.WarmPoolSlot)
		assignment.SandboxID = strings.TrimSpace(assignment.SandboxID)
		assignment.SandboxRole = strings.TrimSpace(assignment.SandboxRole)
		assignment.SandboxLeaseHolder = strings.TrimSpace(assignment.SandboxLeaseHolder)
		if !assignment.SandboxLeaseExpires.IsZero() {
			assignment.SandboxLeaseExpires = assignment.SandboxLeaseExpires.UTC()
		}
		assignment.ImageRef = strings.TrimSpace(assignment.ImageRef)
		assignment.AntiAffinityKey = strings.TrimSpace(assignment.AntiAffinityKey)
		assignment.RequiredLabels = cloneLabels(assignment.RequiredLabels)
		if assignment.Status == "" {
			assignment.Status = "pending"
		}
		s.assignments[id] = assignment
	}
	for _, pool := range file.WarmPools {
		pool, err := normalizeWarmPool(pool, time.Time{})
		if err != nil {
			continue
		}
		s.warmPools[pool.Name] = pool
	}
	for _, event := range file.AuditEvents {
		event = normalizeAuditEvent(event)
		if event.ID == "" || event.Action == "" || event.Time.IsZero() {
			continue
		}
		s.audit = append(s.audit, event)
	}
	s.audit = chainLegacyAuditEvents(s.audit)
	for _, record := range file.MeteringRecords {
		record = normalizeSandboxMeteringRecord(record)
		if record.ID == "" || record.SandboxID == "" || record.AssignmentID == "" || record.Time.IsZero() {
			continue
		}
		s.metering = append(s.metering, record)
	}
	for _, account := range file.ServiceAccounts {
		account = normalizeServiceAccountRecord(account)
		if account.Name == "" || account.TokenHash == "" || account.Role == "" {
			continue
		}
		s.accounts[account.Name] = account
	}
	for _, binding := range file.OIDCBindings {
		binding, err := normalizeOIDCBindingRecord(binding)
		if err != nil {
			continue
		}
		s.oidcBindings[binding.Name] = binding
	}
	return s, nil
}

func NewMemoryStore(ttl time.Duration) *Store {
	s, _ := OpenStore("", ttl)
	return s
}

func (s *Store) SetAssignmentTTL(ttl time.Duration) {
	if ttl <= 0 {
		ttl = DefaultAssignmentTTL
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.assignmentTTL = ttl
}

func (s *Store) Reconcile() (ReconcileResult, error) {
	return s.ReconcileActor("controller")
}

func (s *Store) ReconcileActor(actor string) (ReconcileResult, error) {
	now := s.now().UTC()
	actor = normalizeActor(actor)
	s.mu.Lock()
	defer s.mu.Unlock()
	result := s.reconcileLocked(now)
	if !result.changed() {
		return result, nil
	}
	s.appendAuditLocked(now, AuditEvent{
		Actor:  actor,
		Action: "fleet.reconcile",
		Fields: map[string]string{
			"stale_workers":        strconv.Itoa(len(result.StaleWorkers)),
			"requeued_assignments": strconv.Itoa(len(result.RequeuedAssignments)),
			"replaced_assignments": strconv.Itoa(len(result.ReplacedAssignments)),
			"warm_pool_created":    strconv.Itoa(len(result.WarmPoolAssignments)),
			"warm_pool_canceled":   strconv.Itoa(len(result.WarmPoolCanceled)),
			"warm_pool_cleanup":    strconv.Itoa(len(result.WarmPoolCleanup)),
		},
	})
	if err := s.persistLocked(); err != nil {
		return result, err
	}
	return result, nil
}

func (s *Store) UpsertHeartbeat(h WorkerHeartbeat) (HostRecord, error) {
	id := strings.TrimSpace(h.ID)
	if id == "" {
		return HostRecord{}, fmt.Errorf("worker id required")
	}
	if h.CPUs < 0 || h.VMs < 0 || h.MaxVMs < 0 || h.Images < 0 {
		return HostRecord{}, fmt.Errorf("worker capacity must not be negative")
	}
	now := s.now().UTC()
	record := HostRecord{
		ID:        id,
		Host:      strings.TrimSpace(h.Host),
		Address:   strings.TrimSpace(h.Address),
		Version:   strings.TrimSpace(h.Version),
		Labels:    cloneLabels(h.Labels),
		ImageRefs: sortedUniqueStrings(h.ImageRefs),
		Capacity:  h.Capacity,
		Status:    "ready",
		LastSeen:  now,
		Expires:   now.Add(s.ttl),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	_, existed := s.hosts[id]
	if old, ok := s.hosts[id]; ok {
		if record.Host == "" {
			record.Host = old.Host
		}
		record.Cordoned = old.Cordoned
		record.CordonReason = old.CordonReason
		record.CordonedAt = old.CordonedAt
		if record.Cordoned {
			record.Status = "cordoned"
		}
	}
	s.hosts[id] = record
	if !existed {
		s.appendAuditLocked(now, AuditEvent{
			Actor:      "worker:" + id,
			Action:     "worker.register",
			TargetType: "worker",
			TargetID:   id,
			WorkerID:   id,
		})
	}
	if err := s.persistLocked(); err != nil {
		return HostRecord{}, err
	}
	return record, nil
}

func (s *Store) CordonWorker(id, reason string) (HostRecord, error) {
	return s.CordonWorkerActor("controller", id, reason)
}

func (s *Store) CordonWorkerActor(actor, id, reason string) (HostRecord, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return HostRecord{}, fmt.Errorf("worker id required")
	}
	now := s.now().UTC()
	actor = normalizeActor(actor)
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.hosts[id]
	if !ok {
		return HostRecord{}, fmt.Errorf("worker %q not registered", id)
	}
	record.Cordoned = true
	record.CordonReason = strings.TrimSpace(reason)
	record.CordonedAt = now
	if now.After(record.Expires) {
		record.Status = "stale"
	} else {
		record.Status = "cordoned"
	}
	s.hosts[id] = record
	s.appendAuditLocked(now, AuditEvent{
		Actor:      actor,
		Action:     "worker.cordon",
		TargetType: "worker",
		TargetID:   id,
		WorkerID:   id,
		Fields:     map[string]string{"reason": record.CordonReason},
	})
	if err := s.persistLocked(); err != nil {
		return HostRecord{}, err
	}
	return s.statusLocked(record), nil
}

func (s *Store) UncordonWorker(id string) (HostRecord, error) {
	return s.UncordonWorkerActor("controller", id)
}

func (s *Store) UncordonWorkerActor(actor, id string) (HostRecord, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return HostRecord{}, fmt.Errorf("worker id required")
	}
	now := s.now().UTC()
	actor = normalizeActor(actor)
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.hosts[id]
	if !ok {
		return HostRecord{}, fmt.Errorf("worker %q not registered", id)
	}
	record.Cordoned = false
	record.CordonReason = ""
	record.CordonedAt = time.Time{}
	if now.After(record.Expires) {
		record.Status = "stale"
	} else {
		record.Status = "ready"
	}
	s.hosts[id] = record
	s.appendAuditLocked(now, AuditEvent{
		Actor:      actor,
		Action:     "worker.uncordon",
		TargetType: "worker",
		TargetID:   id,
		WorkerID:   id,
	})
	if err := s.persistLocked(); err != nil {
		return HostRecord{}, err
	}
	return s.statusLocked(record), nil
}

func (s *Store) Report(r WorkerReport) (HostRecord, error) {
	id := strings.TrimSpace(r.ID)
	if id == "" {
		return HostRecord{}, fmt.Errorf("worker id required")
	}
	status := strings.TrimSpace(r.Status)
	if status == "" {
		return HostRecord{}, fmt.Errorf("report status required")
	}
	received := s.now().UTC()
	if r.Time.IsZero() {
		r.Time = received
	} else {
		r.Time = r.Time.UTC()
	}
	r.AssignmentID = strings.TrimSpace(r.AssignmentID)

	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.hosts[id]
	if !ok {
		return HostRecord{}, fmt.Errorf("worker %q not registered", id)
	}
	r.ID = id
	r.Status = status
	if r.AssignmentID != "" {
		if assignment, ok := s.assignments[r.AssignmentID]; ok {
			if assignment.LeasedTo == "" {
				return HostRecord{}, fmt.Errorf("assignment %q is not leased to %q", r.AssignmentID, id)
			}
			if assignment.LeasedTo != id {
				return HostRecord{}, fmt.Errorf("assignment %q leased to %q", r.AssignmentID, assignment.LeasedTo)
			}
		}
	}
	record.Report = &r
	if record.Cordoned {
		record.Status = "cordoned"
	} else {
		record.Status = "ready"
	}
	record.LastSeen = received
	record.Expires = received.Add(s.ttl)
	s.hosts[id] = record
	if r.AssignmentID != "" {
		assignment, ok := s.assignments[r.AssignmentID]
		if ok {
			storedStatus := status
			if assignment.WarmPool != "" && assignment.Status == "claimed" && (status == "ready" || status == "running") {
				storedStatus = "claimed"
			}
			if assignment.WarmPool != "" && assignment.Status == "draining" && (status == "ready" || status == "running") {
				storedStatus = "draining"
			}
			if assignment.SandboxID != "" && assignment.SandboxRole == sandboxRoleRun && sandboxPendingStopStatus(assignment.Status) {
				storedStatus = "draining"
				if assignment.Status == "restarting" {
					storedStatus = "restarting"
				}
			}
			if assignment.SandboxID != "" && assignment.SandboxRole == sandboxRoleRun {
				s.appendSandboxMeteringLocked(received, assignment)
			}
			assignment.Status = storedStatus
			assignment.Updated = received
			assignment.LastReport = &r
			if assignmentLeaseStatus(status) {
				assignment.LeasedTo = id
				assignment.LeaseExpires = received.Add(s.assignmentTTL)
			}
			s.assignments[assignment.ID] = assignment
			if assignment.SandboxID != "" && assignment.SandboxRole == sandboxRoleStop {
				s.finishSandboxStopLocked(received, assignment.SandboxID, storedStatus)
			}
			if auditReportStatus(storedStatus) {
				s.appendAuditLocked(received, AuditEvent{
					Actor:        "worker:" + id,
					Namespace:    assignment.Namespace,
					Action:       "assignment.report",
					TargetType:   "assignment",
					TargetID:     assignment.ID,
					WorkerID:     id,
					AssignmentID: assignment.ID,
					Status:       storedStatus,
					Fields:       map[string]string{"exit_code": strconv.Itoa(r.ExitCode)},
				})
			}
		}
	}
	if err := s.persistLocked(); err != nil {
		return HostRecord{}, err
	}
	return s.statusLocked(record), nil
}

func (s *Store) CreateAssignment(a Assignment) (Assignment, error) {
	return s.CreateAssignmentActor("controller", a)
}

func (s *Store) CreateAssignmentActor(actor string, a Assignment) (Assignment, error) {
	verb := strings.TrimSpace(a.Verb)
	if verb == "" {
		return Assignment{}, fmt.Errorf("assignment verb required")
	}
	workerID := strings.TrimSpace(a.WorkerID)
	policy, imageRef, antiAffinityKey, requiredLabels, resources, err := normalizePlacementFields(a)
	if err != nil {
		return Assignment{}, err
	}
	now := s.now().UTC()
	actor = normalizeActor(actor)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.reconcileLocked(now)
	id := strings.TrimSpace(a.ID)
	if id == "" {
		id = s.nextAssignmentIDLocked(now)
	}
	if _, ok := s.assignments[id]; ok {
		return Assignment{}, fmt.Errorf("assignment %q already exists", id)
	}
	if workerID != "" {
		if _, ok := s.hosts[workerID]; !ok {
			return Assignment{}, fmt.Errorf("worker %q not registered", workerID)
		}
	} else if policy != "" || len(requiredLabels) > 0 {
		selected, err := s.selectWorkerLocked(policy, imageRef, requiredLabels, antiAffinityKey, resources)
		if err != nil {
			return Assignment{}, err
		}
		workerID = selected
	}
	a.ID = id
	a.Namespace = normalizeNamespace(a.Namespace)
	a.WorkerID = workerID
	a.WarmPool = strings.TrimSpace(a.WarmPool)
	a.WarmPoolSlot = strings.TrimSpace(a.WarmPoolSlot)
	a.SandboxID = strings.TrimSpace(a.SandboxID)
	a.SandboxRole = strings.TrimSpace(a.SandboxRole)
	a.SandboxLeaseHolder = ""
	a.SandboxLeaseExpires = time.Time{}
	a.Policy = policy
	a.ImageRef = imageRef
	a.AntiAffinityKey = antiAffinityKey
	a.RequiredLabels = requiredLabels
	a.Resources = resources
	a.Verb = verb
	a.Args = cloneStrings(a.Args)
	a.Status = "pending"
	a.Created = now
	a.Updated = now
	a.LeasedTo = ""
	a.LeaseExpires = time.Time{}
	a.LastReport = nil
	s.assignments[id] = a
	s.appendAuditLocked(now, AuditEvent{
		Actor:        actor,
		Namespace:    a.Namespace,
		Action:       "assignment.create",
		TargetType:   "assignment",
		TargetID:     id,
		WorkerID:     workerID,
		AssignmentID: id,
		Fields: map[string]string{
			"verb":      verb,
			"policy":    policy,
			"image_ref": imageRef,
		},
	})
	if err := s.persistLocked(); err != nil {
		return Assignment{}, err
	}
	return cloneAssignment(a), nil
}

func (s *Store) PlanAssignment(a Assignment, limit int) (PlacementPlan, error) {
	policy, imageRef, antiAffinityKey, requiredLabels, resources, err := normalizePlacementFields(a)
	if err != nil {
		return PlacementPlan{}, err
	}
	if policy == "" {
		policy = PolicyLeastLoaded
	}
	if limit <= 0 {
		limit = DefaultPlacementPlanLimit
	}
	now := s.now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()
	reconciled := s.reconcileLocked(now)
	candidates := s.placementCandidatesLocked(policy, imageRef, requiredLabels, antiAffinityKey, resources)
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	plan := PlacementPlan{
		Namespace:       normalizeNamespace(a.Namespace),
		Policy:          policy,
		ImageRef:        imageRef,
		RequiredLabels:  cloneLabels(requiredLabels),
		AntiAffinityKey: antiAffinityKey,
		Resources:       normalizeResources(resources),
		Candidates:      clonePlacementCandidates(candidates),
	}
	if reconciled.changed() {
		if err := s.persistLocked(); err != nil {
			return plan, err
		}
	}
	return plan, nil
}

func (s *Store) EnsureWarmPool(req WarmPoolRequest) (WarmPoolResult, error) {
	return s.EnsureWarmPoolActor("controller", req)
}

func (s *Store) EnsureWarmPoolActor(actor string, req WarmPoolRequest) (WarmPoolResult, error) {
	now := s.now().UTC()
	actor = normalizeActor(actor)
	pool, err := warmPoolFromRequest(req, now)
	if err != nil {
		return WarmPoolResult{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.warmPools[pool.Name]; ok && !existing.Created.IsZero() {
		if existing.Namespace != pool.Namespace {
			return WarmPoolResult{}, fmt.Errorf("warm pool %q already exists in another namespace", pool.Name)
		}
		pool.Created = existing.Created
	}
	s.warmPools[pool.Name] = pool
	downsized := s.downsizeWarmPoolLocked(now, pool)
	created := s.ensureWarmPoolLocked(now, pool)
	status := s.warmPoolStatusLocked(pool.Name)
	s.appendAuditLocked(now, AuditEvent{
		Actor:      actor,
		Namespace:  pool.Namespace,
		Action:     "warm_pool.ensure",
		TargetType: "warm_pool",
		TargetID:   pool.Name,
		Fields: map[string]string{
			"size":      strconv.Itoa(pool.Size),
			"created":   strconv.Itoa(len(created)),
			"canceled":  strconv.Itoa(len(downsized.canceled)),
			"cleanup":   strconv.Itoa(len(downsized.cleanup)),
			"image_ref": pool.ImageRef,
		},
	})
	if err := s.persistLocked(); err != nil {
		return WarmPoolResult{Pool: status}, err
	}
	return WarmPoolResult{
		Pool:     status,
		Created:  cloneAssignments(created),
		Canceled: cloneStrings(downsized.canceled),
		Cleanup:  cloneAssignments(downsized.cleanup),
	}, nil
}

func (s *Store) GetWarmPool(name string) (WarmPoolStatus, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return WarmPoolStatus{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.warmPools[name]; !ok {
		return WarmPoolStatus{}, false
	}
	return s.warmPoolStatusLocked(name), true
}

func (s *Store) DeleteWarmPool(name string) (WarmPoolDeleteResult, error) {
	return s.DeleteWarmPoolActor("controller", name)
}

func (s *Store) DeleteWarmPoolActor(actor, name string) (WarmPoolDeleteResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return WarmPoolDeleteResult{}, fmt.Errorf("warm pool name required")
	}
	now := s.now().UTC()
	actor = normalizeActor(actor)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reconcileLocked(now)
	if _, ok := s.warmPools[name]; !ok {
		return WarmPoolDeleteResult{}, fmt.Errorf("warm pool %q not found", name)
	}
	namespace := s.warmPools[name].Namespace
	delete(s.warmPools, name)
	retired := s.retireWarmPoolLocked(now, name)
	s.appendAuditLocked(now, AuditEvent{
		Actor:      actor,
		Namespace:  namespace,
		Action:     "warm_pool.delete",
		TargetType: "warm_pool",
		TargetID:   name,
		Fields: map[string]string{
			"canceled": strconv.Itoa(len(retired.canceled)),
			"cleanup":  strconv.Itoa(len(retired.cleanup)),
			"deferred": strconv.Itoa(len(retired.deferred)),
		},
	})
	if err := s.persistLocked(); err != nil {
		return WarmPoolDeleteResult{}, err
	}
	return WarmPoolDeleteResult{
		Namespace: namespace,
		Pool:      name,
		Canceled:  cloneStrings(retired.canceled),
		Cleanup:   cloneAssignments(retired.cleanup),
		Deferred:  cloneStrings(retired.deferred),
	}, nil
}

func (s *Store) ClaimWarmPool(req WarmPoolClaimRequest) (WarmPoolClaimResult, error) {
	return s.ClaimWarmPoolActor("controller", req)
}

func (s *Store) ClaimWarmPoolActor(actor string, req WarmPoolClaimRequest) (WarmPoolClaimResult, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return WarmPoolClaimResult{}, fmt.Errorf("warm pool name required")
	}
	command := cloneStrings(req.Command)
	if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
		return WarmPoolClaimResult{}, fmt.Errorf("warm pool claim command required")
	}
	env, err := normalizeEnv(req.Env)
	if err != nil {
		return WarmPoolClaimResult{}, err
	}
	now := s.now().UTC()
	actor = normalizeActor(actor)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.reconcileLocked(now)
	pool, ok := s.warmPools[name]
	if !ok {
		return WarmPoolClaimResult{}, fmt.Errorf("warm pool %q not found", name)
	}
	namespace := normalizeNamespace(req.Namespace)
	if namespace != "" && pool.Namespace != namespace {
		return WarmPoolClaimResult{}, fmt.Errorf("warm pool %q not found in namespace %q", name, namespace)
	}
	slot, ok := s.claimableWarmPoolSlotLocked(name)
	if !ok {
		return WarmPoolClaimResult{}, fmt.Errorf("warm pool %q has no ready slot to claim", name)
	}
	vmName := WarmPoolAssignmentVMName(slot)
	slot.Status = "claimed"
	slot.Updated = now
	s.assignments[slot.ID] = slot

	assignment := Assignment{
		ID:           s.nextAssignmentIDLocked(now),
		Namespace:    pool.Namespace,
		WorkerID:     slot.WorkerID,
		WarmPoolSlot: slot.ID,
		Verb:         "cove",
		Args:         warmPoolClaimArgs(vmName, command, env),
		Status:       "pending",
		Created:      now,
		Updated:      now,
	}
	s.assignments[assignment.ID] = assignment
	s.ensureWarmPoolLocked(now, pool)
	s.appendAuditLocked(now, AuditEvent{
		Actor:        actor,
		Namespace:    pool.Namespace,
		Action:       "warm_pool.claim",
		TargetType:   "warm_pool",
		TargetID:     name,
		WorkerID:     slot.WorkerID,
		AssignmentID: assignment.ID,
		Fields: map[string]string{
			"slot":    slot.ID,
			"vm_name": vmName,
		},
	})
	if err := s.persistLocked(); err != nil {
		return WarmPoolClaimResult{}, err
	}
	return WarmPoolClaimResult{
		Namespace:  pool.Namespace,
		Pool:       name,
		VMName:     vmName,
		Slot:       cloneAssignment(slot),
		Assignment: cloneAssignment(assignment),
	}, nil
}

func (s *Store) CreateSandbox(req SandboxRequest) (SandboxStatus, error) {
	return s.CreateSandboxActor("controller", req)
}

func (s *Store) CreateSandboxActor(actor string, req SandboxRequest) (SandboxStatus, error) {
	imageRef := strings.TrimSpace(req.ImageRef)
	if imageRef == "" {
		return SandboxStatus{}, fmt.Errorf("sandbox image_ref required")
	}
	args := cloneStrings(req.Args)
	if err := validateForkRunArgs(args, "sandbox"); err != nil {
		return SandboxStatus{}, err
	}
	assignment := Assignment{
		Namespace:       normalizeNamespace(req.Namespace),
		Policy:          strings.TrimSpace(req.Policy),
		ImageRef:        imageRef,
		RequiredLabels:  cloneLabels(req.RequiredLabels),
		AntiAffinityKey: strings.TrimSpace(req.AntiAffinityKey),
		Resources:       req.Resources,
	}
	policy, imageRef, antiAffinityKey, requiredLabels, resources, err := normalizePlacementFields(assignment)
	if err != nil {
		return SandboxStatus{}, err
	}
	if policy == "" {
		policy = PolicyImageAffinity
	}
	now := s.now().UTC()
	actor = normalizeActor(actor)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.reconcileLocked(now)
	id := strings.TrimSpace(req.ID)
	if id == "" {
		id = s.nextSandboxIDLocked(now)
	}
	if strings.Contains(id, "/") {
		return SandboxStatus{}, fmt.Errorf("sandbox id must not contain /")
	}
	if _, ok := s.sandboxRunAssignmentLocked(id); ok {
		return SandboxStatus{}, fmt.Errorf("sandbox %q already exists", id)
	}
	if _, ok := s.assignments[id]; ok {
		return SandboxStatus{}, fmt.Errorf("assignment %q already exists", id)
	}
	workerID, err := s.selectWorkerLocked(policy, imageRef, requiredLabels, antiAffinityKey, resources)
	if err != nil {
		return SandboxStatus{}, err
	}
	vmName := strings.TrimSpace(req.VMName)
	if vmName == "" {
		vmName = sandboxVMName(id)
	}
	assignment = Assignment{
		ID:              id,
		Namespace:       normalizeNamespace(req.Namespace),
		WorkerID:        workerID,
		SandboxID:       id,
		SandboxRole:     sandboxRoleRun,
		Policy:          policy,
		ImageRef:        imageRef,
		RequiredLabels:  requiredLabels,
		AntiAffinityKey: antiAffinityKey,
		Resources:       normalizeResources(resources),
		Verb:            "cove",
		Args:            sandboxRunArgs(imageRef, vmName, args),
		Status:          "pending",
		Created:         now,
		Updated:         now,
	}
	s.assignments[id] = assignment
	s.appendAuditLocked(now, AuditEvent{
		Actor:        actor,
		Namespace:    assignment.Namespace,
		Action:       "sandbox.create",
		TargetType:   "sandbox",
		TargetID:     id,
		WorkerID:     workerID,
		AssignmentID: id,
		Fields: map[string]string{
			"image_ref": imageRef,
			"vm_name":   vmName,
			"policy":    policy,
		},
	})
	if err := s.persistLocked(); err != nil {
		return SandboxStatus{}, err
	}
	return sandboxStatusFromAssignment(assignment, now), nil
}

func (s *Store) GetSandbox(id string) (SandboxStatus, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return SandboxStatus{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	assignment, ok := s.sandboxRunAssignmentLocked(id)
	if !ok {
		return SandboxStatus{}, false
	}
	return sandboxStatusFromAssignment(assignment, s.now().UTC()), true
}

func (s *Store) ListSandboxes() []SandboxStatus {
	return s.ListSandboxesNamespace("")
}

func (s *Store) ListSandboxesNamespace(namespace string) []SandboxStatus {
	namespace = normalizeNamespace(namespace)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	var sandboxes []SandboxStatus
	for _, assignment := range s.sortedAssignmentsLocked() {
		if assignment.SandboxID == "" || assignment.SandboxRole != sandboxRoleRun {
			continue
		}
		if !namespaceMatches(assignment.Namespace, namespace) {
			continue
		}
		sandboxes = append(sandboxes, sandboxStatusFromAssignment(assignment, now))
	}
	return sandboxes
}

func (s *Store) ListSandboxMetering(namespace, sandboxID string) SandboxMeteringResult {
	namespace = normalizeNamespace(namespace)
	sandboxID = strings.TrimSpace(sandboxID)
	s.mu.Lock()
	defer s.mu.Unlock()
	records := make([]SandboxMeteringRecord, 0, len(s.metering))
	for _, record := range s.sortedMeteringLocked() {
		if !namespaceMatches(record.Namespace, namespace) {
			continue
		}
		if sandboxID != "" && record.SandboxID != sandboxID {
			continue
		}
		records = append(records, cloneSandboxMeteringRecord(record))
	}
	return SandboxMeteringResult{
		Records: records,
		Summary: sandboxMeteringSummary(namespace, sandboxID, records),
	}
}

func (s *Store) ExecSandbox(id string, req SandboxExecRequest) (SandboxExecResult, error) {
	return s.ExecSandboxActor("controller", id, req)
}

func (s *Store) ExecSandboxActor(actor, id string, req SandboxExecRequest) (SandboxExecResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return SandboxExecResult{}, fmt.Errorf("sandbox id required")
	}
	command := cloneStrings(req.Command)
	if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
		return SandboxExecResult{}, fmt.Errorf("sandbox exec command required")
	}
	env, err := normalizeEnv(req.Env)
	if err != nil {
		return SandboxExecResult{}, err
	}
	now := s.now().UTC()
	actor = normalizeActor(actor)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.reconcileLocked(now)
	sandbox, ok := s.sandboxRunAssignmentLocked(id)
	if !ok {
		return SandboxExecResult{}, fmt.Errorf("sandbox %q not found", id)
	}
	if sandbox.Status != "ready" {
		return SandboxExecResult{}, fmt.Errorf("sandbox %q is %s", id, sandbox.Status)
	}
	if err := s.requireSandboxWorkerReadyLocked(now, sandbox.WorkerID); err != nil {
		return SandboxExecResult{}, err
	}
	vmName := SandboxAssignmentVMName(sandbox)
	assignment := Assignment{
		ID:          s.nextAssignmentIDLocked(now),
		Namespace:   sandbox.Namespace,
		WorkerID:    sandbox.WorkerID,
		SandboxID:   id,
		SandboxRole: sandboxRoleExec,
		Verb:        "cove",
		Args:        sandboxExecArgs(vmName, command, env),
		Status:      "pending",
		Created:     now,
		Updated:     now,
	}
	s.assignments[assignment.ID] = assignment
	s.appendAuditLocked(now, AuditEvent{
		Actor:        actor,
		Namespace:    sandbox.Namespace,
		Action:       "sandbox.exec",
		TargetType:   "sandbox",
		TargetID:     id,
		WorkerID:     sandbox.WorkerID,
		AssignmentID: assignment.ID,
		Fields: map[string]string{
			"vm_name": vmName,
			"argc":    strconv.Itoa(len(command)),
		},
	})
	if err := s.persistLocked(); err != nil {
		return SandboxExecResult{}, err
	}
	return sandboxExecResult(id, vmName, assignment), nil
}

func (s *Store) ControlSandbox(id string, req SandboxControlRequest) (SandboxControlResult, error) {
	return s.ControlSandboxActor("controller", id, req)
}

func (s *Store) ControlSandboxActor(actor, id string, req SandboxControlRequest) (SandboxControlResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return SandboxControlResult{}, fmt.Errorf("sandbox id required")
	}
	payload, typ, err := sandboxControlPayload(req)
	if err != nil {
		return SandboxControlResult{}, err
	}
	now := s.now().UTC()
	actor = normalizeActor(actor)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.reconcileLocked(now)
	sandbox, ok := s.sandboxRunAssignmentLocked(id)
	if !ok {
		return SandboxControlResult{}, fmt.Errorf("sandbox %q not found", id)
	}
	if sandbox.Status != "ready" {
		return SandboxControlResult{}, fmt.Errorf("sandbox %q is %s", id, sandbox.Status)
	}
	if err := s.requireSandboxWorkerReadyLocked(now, sandbox.WorkerID); err != nil {
		return SandboxControlResult{}, err
	}
	vmName := SandboxAssignmentVMName(sandbox)
	assignment := Assignment{
		ID:          s.nextAssignmentIDLocked(now),
		Namespace:   sandbox.Namespace,
		WorkerID:    sandbox.WorkerID,
		SandboxID:   id,
		SandboxRole: sandboxRoleControl,
		Verb:        "cove-control",
		Args:        []string{vmName, string(payload)},
		Status:      "pending",
		Created:     now,
		Updated:     now,
	}
	s.assignments[assignment.ID] = assignment
	s.appendAuditLocked(now, AuditEvent{
		Actor:        actor,
		Namespace:    sandbox.Namespace,
		Action:       "sandbox.control",
		TargetType:   "sandbox",
		TargetID:     id,
		WorkerID:     sandbox.WorkerID,
		AssignmentID: assignment.ID,
		Fields: map[string]string{
			"vm_name": vmName,
			"type":    typ,
		},
	})
	if err := s.persistLocked(); err != nil {
		return SandboxControlResult{}, err
	}
	return sandboxControlResult(id, vmName, typ, assignment), nil
}

func (s *Store) LeaseSandbox(id string, req SandboxLeaseRequest) (SandboxLeaseResult, error) {
	return s.LeaseSandboxActor("controller", id, req)
}

func (s *Store) LeaseSandboxActor(actor, id string, req SandboxLeaseRequest) (SandboxLeaseResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return SandboxLeaseResult{}, fmt.Errorf("sandbox id required")
	}
	now := s.now().UTC()
	actor = normalizeActor(actor)
	holder, expires, err := sandboxLeaseRequest(req, actor, now)
	if err != nil {
		return SandboxLeaseResult{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reconcileLocked(now)
	assignment, ok := s.sandboxRunAssignmentLocked(id)
	if !ok {
		return SandboxLeaseResult{}, fmt.Errorf("sandbox %q not found", id)
	}
	clearExpiredSandboxLease(&assignment, now)
	if assignment.SandboxLeaseHolder != "" && assignment.SandboxLeaseHolder != holder {
		return SandboxLeaseResult{}, fmt.Errorf("sandbox %q lease held by %q", id, assignment.SandboxLeaseHolder)
	}
	assignment.SandboxLeaseHolder = holder
	assignment.SandboxLeaseExpires = expires
	assignment.Updated = now
	s.assignments[assignment.ID] = assignment
	s.appendAuditLocked(now, AuditEvent{
		Actor:        actor,
		Namespace:    assignment.Namespace,
		Action:       "sandbox.lease",
		TargetType:   "sandbox",
		TargetID:     id,
		WorkerID:     assignment.WorkerID,
		AssignmentID: assignment.ID,
		Fields: map[string]string{
			"holder":  holder,
			"expires": expires.Format(time.RFC3339Nano),
		},
	})
	if err := s.persistLocked(); err != nil {
		return SandboxLeaseResult{}, err
	}
	lease := SandboxLease{Holder: holder, Expires: expires}
	return SandboxLeaseResult{Sandbox: sandboxStatusFromAssignment(assignment, now), Lease: lease}, nil
}

func (s *Store) ReleaseSandboxLease(id, holder string) (SandboxStatus, error) {
	return s.ReleaseSandboxLeaseActor("controller", id, holder)
}

func (s *Store) ReleaseSandboxLeaseActor(actor, id, holder string) (SandboxStatus, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return SandboxStatus{}, fmt.Errorf("sandbox id required")
	}
	now := s.now().UTC()
	actor = normalizeActor(actor)
	holder = normalizeSandboxLeaseHolder(holder, actor)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reconcileLocked(now)
	assignment, ok := s.sandboxRunAssignmentLocked(id)
	if !ok {
		return SandboxStatus{}, fmt.Errorf("sandbox %q not found", id)
	}
	expired := clearExpiredSandboxLease(&assignment, now)
	if assignment.SandboxLeaseHolder == "" {
		if expired {
			assignment.Updated = now
			s.assignments[assignment.ID] = assignment
			if err := s.persistLocked(); err != nil {
				return SandboxStatus{}, err
			}
		}
		return sandboxStatusFromAssignment(assignment, now), nil
	}
	if assignment.SandboxLeaseHolder != holder {
		return SandboxStatus{}, fmt.Errorf("sandbox %q lease held by %q", id, assignment.SandboxLeaseHolder)
	}
	assignment.SandboxLeaseHolder = ""
	assignment.SandboxLeaseExpires = time.Time{}
	assignment.Updated = now
	s.assignments[assignment.ID] = assignment
	s.appendAuditLocked(now, AuditEvent{
		Actor:        actor,
		Namespace:    assignment.Namespace,
		Action:       "sandbox.lease.release",
		TargetType:   "sandbox",
		TargetID:     id,
		WorkerID:     assignment.WorkerID,
		AssignmentID: assignment.ID,
		Fields:       map[string]string{"holder": holder},
	})
	if err := s.persistLocked(); err != nil {
		return SandboxStatus{}, err
	}
	return sandboxStatusFromAssignment(assignment, now), nil
}

func (s *Store) StartSandbox(id string) (SandboxStartResult, error) {
	return s.StartSandboxActor("controller", id)
}

func (s *Store) StartSandboxActor(actor, id string) (SandboxStartResult, error) {
	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.startSandboxLocked(now, normalizeActor(actor), id, "sandbox.start")
	if err != nil {
		return SandboxStartResult{}, err
	}
	if result.Started {
		if err := s.persistLocked(); err != nil {
			return SandboxStartResult{}, err
		}
	}
	return result, nil
}

func (s *Store) RestartSandbox(id string) (SandboxRestartResult, error) {
	return s.RestartSandboxActor("controller", id)
}

func (s *Store) RestartSandboxActor(actor, id string) (SandboxRestartResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return SandboxRestartResult{}, fmt.Errorf("sandbox id required")
	}
	now := s.now().UTC()
	actor = normalizeActor(actor)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reconcileLocked(now)
	assignment, ok := s.sandboxRunAssignmentLocked(id)
	if !ok {
		return SandboxRestartResult{}, fmt.Errorf("sandbox %q not found", id)
	}
	if sandboxTerminalStatus(assignment.Status) {
		started, err := s.startSandboxLocked(now, actor, id, "sandbox.restart")
		if err != nil {
			return SandboxRestartResult{}, err
		}
		if started.Started {
			if err := s.persistLocked(); err != nil {
				return SandboxRestartResult{}, err
			}
		}
		return SandboxRestartResult{
			Namespace:  started.Namespace,
			ID:         started.ID,
			VMName:     started.VMName,
			Status:     started.Status,
			Restarting: started.Started,
			Assignment: started.Assignment,
		}, nil
	}
	vmName := SandboxAssignmentVMName(assignment)
	result := SandboxRestartResult{
		Namespace:  assignment.Namespace,
		ID:         id,
		VMName:     vmName,
		Status:     assignment.Status,
		Assignment: cloneAssignment(assignment),
	}
	switch assignment.Status {
	case "pending":
		return result, nil
	case "leased", "running", "ready", "draining", "restarting":
		cleanup, ok := s.activeSandboxCleanupLocked(id)
		if !ok {
			cleanup = Assignment{
				ID:          s.nextAssignmentIDLocked(now),
				Namespace:   assignment.Namespace,
				WorkerID:    assignment.WorkerID,
				SandboxID:   id,
				SandboxRole: sandboxRoleStop,
				Verb:        "cove",
				Args:        sandboxStopArgs(vmName),
				Status:      "pending",
				Created:     now,
				Updated:     now,
			}
			s.assignments[cleanup.ID] = cleanup
		}
		s.appendSandboxMeteringLocked(now, assignment)
		assignment.Status = "restarting"
		assignment.Updated = now
		s.assignments[assignment.ID] = assignment
		result.Status = assignment.Status
		result.Restarting = true
		result.Assignment = cloneAssignment(assignment)
		cleanup = cloneAssignment(cleanup)
		result.Cleanup = &cleanup
	}
	s.appendAuditLocked(now, AuditEvent{
		Actor:        actor,
		Namespace:    assignment.Namespace,
		Action:       "sandbox.restart",
		TargetType:   "sandbox",
		TargetID:     id,
		WorkerID:     assignment.WorkerID,
		AssignmentID: assignment.ID,
		Fields: map[string]string{
			"vm_name":    vmName,
			"status":     result.Status,
			"restarting": strconv.FormatBool(result.Restarting),
			"cleanup":    strconv.FormatBool(result.Cleanup != nil),
		},
	})
	if err := s.persistLocked(); err != nil {
		return SandboxRestartResult{}, err
	}
	return result, nil
}

func (s *Store) DeleteSandbox(id string) (SandboxDeleteResult, error) {
	return s.DeleteSandboxActor("controller", id)
}

func (s *Store) DeleteSandboxActor(actor, id string) (SandboxDeleteResult, error) {
	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.stopSandboxLocked(now, normalizeActor(actor), id, "sandbox.delete")
	if err != nil {
		return SandboxDeleteResult{}, err
	}
	if err := s.persistLocked(); err != nil {
		return SandboxDeleteResult{}, err
	}
	return SandboxDeleteResult(result), nil
}

func (s *Store) StopSandbox(id string) (SandboxStopResult, error) {
	return s.StopSandboxActor("controller", id)
}

func (s *Store) StopSandboxActor(actor, id string) (SandboxStopResult, error) {
	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.stopSandboxLocked(now, normalizeActor(actor), id, "sandbox.stop")
	if err != nil {
		return SandboxStopResult{}, err
	}
	if err := s.persistLocked(); err != nil {
		return SandboxStopResult{}, err
	}
	return result, nil
}

func (s *Store) WaitSandbox(id string) (SandboxWaitResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return SandboxWaitResult{}, fmt.Errorf("sandbox id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	assignment, ok := s.sandboxRunAssignmentLocked(id)
	if !ok {
		return SandboxWaitResult{}, fmt.Errorf("sandbox %q not found", id)
	}
	sandbox := sandboxStatusFromAssignment(assignment, s.now().UTC())
	return SandboxWaitResult{
		Done:    sandboxTerminalStatus(sandbox.Status),
		Sandbox: sandbox,
	}, nil
}

func (s *Store) PrepareImage(req ImagePrepareRequest) (ImagePrepareResult, error) {
	return s.PrepareImageActor("controller", req)
}

func (s *Store) PrepareImageActor(actor string, req ImagePrepareRequest) (ImagePrepareResult, error) {
	sourceRef := strings.TrimSpace(req.SourceRef)
	imageRef := strings.TrimSpace(req.ImageRef)
	if sourceRef == "" {
		return ImagePrepareResult{}, fmt.Errorf("image prepare source_ref required")
	}
	if imageRef == "" {
		return ImagePrepareResult{}, fmt.Errorf("image prepare image_ref required")
	}
	namespace := normalizeNamespace(req.Namespace)
	labels := cloneLabels(req.RequiredLabels)
	now := s.now().UTC()
	actor = normalizeActor(actor)

	s.mu.Lock()
	defer s.mu.Unlock()
	reconciled := s.reconcileLocked(now)
	result := ImagePrepareResult{
		Namespace: namespace,
		SourceRef: sourceRef,
		ImageRef:  imageRef,
	}
	for _, host := range s.sortedHostsLocked() {
		host = s.statusLocked(host)
		if !labelsMatch(host.Labels, labels) {
			continue
		}
		if host.Status != "ready" {
			result.Skipped = append(result.Skipped, ImagePrepareSkip{WorkerID: host.ID, Reason: host.Status})
			continue
		}
		if containsString(host.ImageRefs, imageRef) {
			result.Skipped = append(result.Skipped, ImagePrepareSkip{WorkerID: host.ID, Reason: "present"})
			continue
		}
		if s.activeImagePrepareLocked(host.ID, imageRef) {
			result.Skipped = append(result.Skipped, ImagePrepareSkip{WorkerID: host.ID, Reason: "active"})
			continue
		}
		assignment := Assignment{
			ID:             s.nextAssignmentIDLocked(now),
			Namespace:      namespace,
			WorkerID:       host.ID,
			ImageRef:       imageRef,
			RequiredLabels: labels,
			Verb:           "cove",
			Args:           imagePrepareArgs(sourceRef, imageRef, req.Force),
			Status:         "pending",
			Created:        now,
			Updated:        now,
		}
		s.assignments[assignment.ID] = assignment
		result.Assignments = append(result.Assignments, cloneAssignment(assignment))
	}
	if len(result.Assignments) == 0 && len(result.Skipped) == 0 {
		return result, fmt.Errorf("no workers match image prepare")
	}
	if len(result.Assignments) > 0 || reconciled.changed() {
		if len(result.Assignments) > 0 {
			s.appendAuditLocked(now, AuditEvent{
				Actor:      actor,
				Namespace:  namespace,
				Action:     "image.prepare",
				TargetType: "image",
				TargetID:   imageRef,
				Fields: map[string]string{
					"source_ref":  sourceRef,
					"assignments": strconv.Itoa(len(result.Assignments)),
				},
			})
		}
		if err := s.persistLocked(); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (s *Store) PushImageGC(req ImageGCRequest) (ImageGCResult, error) {
	return s.PushImageGCActor("controller", req)
}

func (s *Store) PushImageGCActor(actor string, req ImageGCRequest) (ImageGCResult, error) {
	namespace := normalizeNamespace(req.Namespace)
	labels := cloneLabels(req.RequiredLabels)
	olderThan, err := normalizeDurationString(req.OlderThan, "image gc older_than")
	if err != nil {
		return ImageGCResult{}, err
	}
	now := s.now().UTC()
	actor = normalizeActor(actor)

	s.mu.Lock()
	defer s.mu.Unlock()
	reconciled := s.reconcileLocked(now)
	result := ImageGCResult{Namespace: namespace}
	for _, host := range s.sortedHostsLocked() {
		host = s.statusLocked(host)
		if !labelsMatch(host.Labels, labels) {
			continue
		}
		if host.Status != "ready" {
			result.Skipped = append(result.Skipped, ImageGCSkip{WorkerID: host.ID, Reason: host.Status})
			continue
		}
		if s.activeImageGCLocked(host.ID) {
			result.Skipped = append(result.Skipped, ImageGCSkip{WorkerID: host.ID, Reason: "active"})
			continue
		}
		assignment := Assignment{
			ID:             s.nextAssignmentIDLocked(now),
			Namespace:      namespace,
			WorkerID:       host.ID,
			RequiredLabels: labels,
			Verb:           "cove",
			Args:           imageGCArgs(olderThan, req.Apply),
			Status:         "pending",
			Created:        now,
			Updated:        now,
		}
		s.assignments[assignment.ID] = assignment
		result.Assignments = append(result.Assignments, cloneAssignment(assignment))
	}
	if len(result.Assignments) == 0 && len(result.Skipped) == 0 {
		return result, fmt.Errorf("no workers match image gc")
	}
	if len(result.Assignments) > 0 || reconciled.changed() {
		if len(result.Assignments) > 0 {
			s.appendAuditLocked(now, AuditEvent{
				Actor:      actor,
				Namespace:  namespace,
				Action:     "image.gc",
				TargetType: "image",
				Fields: map[string]string{
					"assignments": strconv.Itoa(len(result.Assignments)),
					"apply":       strconv.FormatBool(req.Apply),
					"older_than":  olderThan,
				},
			})
		}
		if err := s.persistLocked(); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (s *Store) PushLifecyclePolicy(req LifecyclePolicyRequest) (LifecyclePolicyResult, error) {
	return s.PushLifecyclePolicyActor("controller", req)
}

func (s *Store) PushLifecyclePolicyActor(actor string, req LifecyclePolicyRequest) (LifecyclePolicyResult, error) {
	vmName, args, err := lifecyclePolicyArgs(req)
	if err != nil {
		return LifecyclePolicyResult{}, err
	}
	namespace := normalizeNamespace(req.Namespace)
	labels := cloneLabels(req.RequiredLabels)
	now := s.now().UTC()
	actor = normalizeActor(actor)

	s.mu.Lock()
	defer s.mu.Unlock()
	reconciled := s.reconcileLocked(now)
	result := LifecyclePolicyResult{Namespace: namespace, VMName: vmName}
	for _, host := range s.sortedHostsLocked() {
		host = s.statusLocked(host)
		if !labelsMatch(host.Labels, labels) {
			continue
		}
		if host.Status != "ready" {
			result.Skipped = append(result.Skipped, LifecyclePolicySkip{WorkerID: host.ID, Reason: host.Status})
			continue
		}
		if s.activeLifecyclePolicyLocked(host.ID, vmName) {
			result.Skipped = append(result.Skipped, LifecyclePolicySkip{WorkerID: host.ID, Reason: "active"})
			continue
		}
		assignment := Assignment{
			ID:             s.nextAssignmentIDLocked(now),
			Namespace:      namespace,
			WorkerID:       host.ID,
			RequiredLabels: labels,
			Verb:           "cove",
			Args:           cloneStrings(args),
			Status:         "pending",
			Created:        now,
			Updated:        now,
		}
		s.assignments[assignment.ID] = assignment
		result.Assignments = append(result.Assignments, cloneAssignment(assignment))
	}
	if len(result.Assignments) == 0 && len(result.Skipped) == 0 {
		return result, fmt.Errorf("no workers match lifecycle policy")
	}
	if len(result.Assignments) > 0 || reconciled.changed() {
		if len(result.Assignments) > 0 {
			s.appendAuditLocked(now, AuditEvent{
				Actor:      actor,
				Namespace:  namespace,
				Action:     "policy.lifecycle",
				TargetType: "vm",
				TargetID:   vmName,
				Fields: map[string]string{
					"assignments": strconv.Itoa(len(result.Assignments)),
					"clear":       strconv.FormatBool(req.Clear),
				},
			})
		}
		if err := s.persistLocked(); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (s *Store) PushStorageBudget(req StorageBudgetRequest) (StorageBudgetResult, error) {
	return s.PushStorageBudgetActor("controller", req)
}

func (s *Store) PushStorageBudgetActor(actor string, req StorageBudgetRequest) (StorageBudgetResult, error) {
	args, err := storageBudgetArgs(req)
	if err != nil {
		return StorageBudgetResult{}, err
	}
	namespace := normalizeNamespace(req.Namespace)
	labels := cloneLabels(req.RequiredLabels)
	now := s.now().UTC()
	actor = normalizeActor(actor)

	s.mu.Lock()
	defer s.mu.Unlock()
	reconciled := s.reconcileLocked(now)
	result := StorageBudgetResult{Namespace: namespace}
	for _, host := range s.sortedHostsLocked() {
		host = s.statusLocked(host)
		if !labelsMatch(host.Labels, labels) {
			continue
		}
		if host.Status != "ready" {
			result.Skipped = append(result.Skipped, StoragePolicySkip{WorkerID: host.ID, Reason: host.Status})
			continue
		}
		if s.activeStorageBudgetLocked(host.ID) {
			result.Skipped = append(result.Skipped, StoragePolicySkip{WorkerID: host.ID, Reason: "active"})
			continue
		}
		assignment := Assignment{
			ID:             s.nextAssignmentIDLocked(now),
			Namespace:      namespace,
			WorkerID:       host.ID,
			RequiredLabels: labels,
			Verb:           "cove",
			Args:           cloneStrings(args),
			Status:         "pending",
			Created:        now,
			Updated:        now,
		}
		s.assignments[assignment.ID] = assignment
		result.Assignments = append(result.Assignments, cloneAssignment(assignment))
	}
	if len(result.Assignments) == 0 && len(result.Skipped) == 0 {
		return result, fmt.Errorf("no workers match storage budget")
	}
	if len(result.Assignments) > 0 || reconciled.changed() {
		if len(result.Assignments) > 0 {
			s.appendAuditLocked(now, AuditEvent{
				Actor:      actor,
				Namespace:  namespace,
				Action:     "storage.budget",
				TargetType: "storage",
				Fields: map[string]string{
					"assignments": strconv.Itoa(len(result.Assignments)),
					"clear":       strconv.FormatBool(req.Clear),
					"target":      strings.TrimSpace(req.Target),
				},
			})
		}
		if err := s.persistLocked(); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (s *Store) PushStoragePrune(req StoragePruneRequest) (StoragePruneResult, error) {
	return s.PushStoragePruneActor("controller", req)
}

func (s *Store) PushStoragePruneActor(actor string, req StoragePruneRequest) (StoragePruneResult, error) {
	args, err := storagePruneArgs(req)
	if err != nil {
		return StoragePruneResult{}, err
	}
	namespace := normalizeNamespace(req.Namespace)
	labels := cloneLabels(req.RequiredLabels)
	now := s.now().UTC()
	actor = normalizeActor(actor)

	s.mu.Lock()
	defer s.mu.Unlock()
	reconciled := s.reconcileLocked(now)
	result := StoragePruneResult{Namespace: namespace}
	for _, host := range s.sortedHostsLocked() {
		host = s.statusLocked(host)
		if !labelsMatch(host.Labels, labels) {
			continue
		}
		if host.Status != "ready" {
			result.Skipped = append(result.Skipped, StoragePolicySkip{WorkerID: host.ID, Reason: host.Status})
			continue
		}
		if s.activeStoragePruneLocked(host.ID) {
			result.Skipped = append(result.Skipped, StoragePolicySkip{WorkerID: host.ID, Reason: "active"})
			continue
		}
		assignment := Assignment{
			ID:             s.nextAssignmentIDLocked(now),
			Namespace:      namespace,
			WorkerID:       host.ID,
			RequiredLabels: labels,
			Verb:           "cove",
			Args:           cloneStrings(args),
			Status:         "pending",
			Created:        now,
			Updated:        now,
		}
		s.assignments[assignment.ID] = assignment
		result.Assignments = append(result.Assignments, cloneAssignment(assignment))
	}
	if len(result.Assignments) == 0 && len(result.Skipped) == 0 {
		return result, fmt.Errorf("no workers match storage prune")
	}
	if len(result.Assignments) > 0 || reconciled.changed() {
		if len(result.Assignments) > 0 {
			s.appendAuditLocked(now, AuditEvent{
				Actor:      actor,
				Namespace:  namespace,
				Action:     "storage.prune",
				TargetType: "storage",
				Fields: map[string]string{
					"assignments": strconv.Itoa(len(result.Assignments)),
					"apply":       strconv.FormatBool(req.Apply),
					"category":    strings.TrimSpace(req.Category),
				},
			})
		}
		if err := s.persistLocked(); err != nil {
			return result, err
		}
	}
	return result, nil
}

func (s *Store) AwaitAssignment(id string) (*Assignment, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("worker id required")
	}
	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.hosts[id]; !ok {
		return nil, fmt.Errorf("worker %q not registered", id)
	}
	worker := s.statusLocked(s.hosts[id])
	reconciled := s.reconcileLocked(now)
	assignments := s.sortedAssignmentsLocked()
	for _, assignment := range assignments {
		direct := assignment.WorkerID == id
		if assignment.WorkerID != "" && !direct {
			continue
		}
		if !direct && worker.Status != "ready" {
			continue
		}
		if assignment.Status != "pending" {
			continue
		}
		assignment.Status = "leased"
		assignment.LeasedTo = id
		assignment.LeaseExpires = now.Add(s.assignmentTTL)
		assignment.Updated = now
		s.assignments[assignment.ID] = assignment
		s.appendAuditLocked(now, AuditEvent{
			Actor:        "worker:" + id,
			Namespace:    assignment.Namespace,
			Action:       "assignment.lease",
			TargetType:   "assignment",
			TargetID:     assignment.ID,
			WorkerID:     id,
			AssignmentID: assignment.ID,
			Status:       "leased",
		})
		if err := s.persistLocked(); err != nil {
			return nil, err
		}
		out := cloneAssignment(assignment)
		return &out, nil
	}
	if reconciled.changed() {
		if err := s.persistLocked(); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

func (s *Store) GetAssignment(id string) (Assignment, bool) {
	id = strings.TrimSpace(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	assignment, ok := s.assignments[id]
	if !ok {
		return Assignment{}, false
	}
	return cloneAssignment(assignment), true
}

func (s *Store) ListAssignments() []Assignment {
	return s.ListAssignmentsNamespace("")
}

func (s *Store) ListAssignmentsNamespace(namespace string) []Assignment {
	namespace = normalizeNamespace(namespace)
	s.mu.Lock()
	defer s.mu.Unlock()
	assignments := s.sortedAssignmentsLocked()
	out := make([]Assignment, 0, len(assignments))
	for _, assignment := range assignments {
		if !namespaceMatches(assignment.Namespace, namespace) {
			continue
		}
		out = append(out, cloneAssignment(assignment))
	}
	return out
}

func (s *Store) ListWarmPools() []WarmPoolStatus {
	return s.ListWarmPoolsNamespace("")
}

func (s *Store) ListWarmPoolsNamespace(namespace string) []WarmPoolStatus {
	namespace = normalizeNamespace(namespace)
	s.mu.Lock()
	defer s.mu.Unlock()
	pools := s.sortedWarmPoolsLocked()
	out := make([]WarmPoolStatus, 0, len(pools))
	for _, pool := range pools {
		if !namespaceMatches(pool.Namespace, namespace) {
			continue
		}
		out = append(out, s.warmPoolStatusLocked(pool.Name))
	}
	return out
}

func (s *Store) ListAudit(limit int) []AuditEvent {
	return s.ListAuditNamespace(limit, "")
}

func (s *Store) ListAuditNamespace(limit int, namespace string) []AuditEvent {
	namespace = normalizeNamespace(namespace)
	s.mu.Lock()
	defer s.mu.Unlock()
	events := s.sortedAuditLocked()
	if namespace != "" {
		filtered := events[:0]
		for _, event := range events {
			if namespaceMatches(event.Namespace, namespace) {
				filtered = append(filtered, event)
			}
		}
		events = filtered
	}
	if limit > 0 && len(events) > limit {
		events = events[len(events)-limit:]
	}
	return cloneAuditEvents(events)
}

func (s *Store) VerifyAudit() AuditVerifyResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	return verifyAuditEvents(cloneAuditEvents(s.audit))
}

func (s *Store) UpsertServiceAccount(req ServiceAccountRequest) (ServiceAccountResult, error) {
	return s.UpsertServiceAccountActor("controller", req)
}

func (s *Store) UpsertServiceAccountActor(actor string, req ServiceAccountRequest) (ServiceAccountResult, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return ServiceAccountResult{}, fmt.Errorf("service account name required")
	}
	token := strings.TrimSpace(req.Token)
	if token == "" {
		return ServiceAccountResult{}, fmt.Errorf("service account token required")
	}
	role, err := normalizeServiceAccountRole(req.Role)
	if err != nil {
		return ServiceAccountResult{}, err
	}
	now := s.now().UTC()
	actor = normalizeActor(actor)
	record := serviceAccountRecord{
		Name:      name,
		Namespace: normalizeNamespace(req.Namespace),
		Role:      role,
		TokenHash: tokenHash(token),
		Created:   now,
		Updated:   now,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.accounts[name]; ok && !old.Created.IsZero() {
		if old.Namespace != record.Namespace {
			return ServiceAccountResult{}, fmt.Errorf("service account %q already exists in another namespace", name)
		}
		record.Created = old.Created
	}
	s.accounts[name] = record
	s.appendAuditLocked(now, AuditEvent{
		Actor:      actor,
		Namespace:  record.Namespace,
		Action:     "service_account.upsert",
		TargetType: "service_account",
		TargetID:   name,
		Fields:     map[string]string{"role": record.Role},
	})
	if err := s.persistLocked(); err != nil {
		return ServiceAccountResult{}, err
	}
	return ServiceAccountResult{ServiceAccount: publicServiceAccount(record)}, nil
}

func (s *Store) DeleteServiceAccount(name string) (ServiceAccountResult, error) {
	return s.DeleteServiceAccountActor("controller", name)
}

func (s *Store) DeleteServiceAccountActor(actor, name string) (ServiceAccountResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ServiceAccountResult{}, fmt.Errorf("service account name required")
	}
	now := s.now().UTC()
	actor = normalizeActor(actor)

	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.accounts[name]
	if !ok {
		return ServiceAccountResult{}, fmt.Errorf("service account %q not found", name)
	}
	delete(s.accounts, name)
	s.appendAuditLocked(now, AuditEvent{
		Actor:      actor,
		Namespace:  record.Namespace,
		Action:     "service_account.delete",
		TargetType: "service_account",
		TargetID:   name,
	})
	if err := s.persistLocked(); err != nil {
		return ServiceAccountResult{}, err
	}
	return ServiceAccountResult{ServiceAccount: publicServiceAccount(record)}, nil
}

func (s *Store) ListServiceAccounts() []ServiceAccount {
	return s.ListServiceAccountsNamespace("")
}

func (s *Store) ListServiceAccountsNamespace(namespace string) []ServiceAccount {
	namespace = normalizeNamespace(namespace)
	s.mu.Lock()
	defer s.mu.Unlock()
	records := s.sortedServiceAccountsLocked()
	out := make([]ServiceAccount, 0, len(records))
	for _, record := range records {
		if !namespaceMatches(record.Namespace, namespace) {
			continue
		}
		out = append(out, publicServiceAccount(record))
	}
	return out
}

func (s *Store) AuthenticateServiceAccount(token string) (ServiceAccount, bool) {
	hash := tokenHash(strings.TrimSpace(token))
	if hash == "" {
		return ServiceAccount{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var match serviceAccountRecord
	ok := false
	for _, record := range s.accounts {
		if subtle.ConstantTimeCompare([]byte(record.TokenHash), []byte(hash)) == 1 {
			match = record
			ok = true
		}
	}
	if ok {
		return publicServiceAccount(match), true
	}
	return ServiceAccount{}, false
}

type authenticatedPrincipal struct {
	Actor     string
	Namespace string
	Role      string
}

func (s *Store) AuthenticateBearer(token string) (authenticatedPrincipal, bool) {
	token = strings.TrimSpace(token)
	if account, ok := s.AuthenticateServiceAccount(token); ok {
		return authenticatedPrincipal{
			Actor:     "service-account:" + account.Name,
			Namespace: normalizeNamespace(account.Namespace),
			Role:      account.Role,
		}, true
	}
	return s.authenticateOIDCBearer(token)
}

func (s *Store) UpsertOIDCBinding(req OIDCBindingRequest) (OIDCBindingResult, error) {
	return s.UpsertOIDCBindingActor("controller", req)
}

func (s *Store) UpsertOIDCBindingActor(actor string, req OIDCBindingRequest) (OIDCBindingResult, error) {
	record := oidcBindingRecord{
		Name:      strings.TrimSpace(req.Name),
		Issuer:    strings.TrimSpace(req.Issuer),
		Subject:   strings.TrimSpace(req.Subject),
		Audience:  strings.TrimSpace(req.Audience),
		Namespace: normalizeNamespace(req.Namespace),
		Role:      strings.TrimSpace(req.Role),
		JWKSURL:   strings.TrimSpace(req.JWKSURL),
		Keys:      oidcRequestKeys(req.Keys),
	}
	var err error
	record, err = normalizeOIDCBindingRecord(record)
	if err != nil {
		return OIDCBindingResult{}, err
	}
	now := s.now().UTC()
	actor = normalizeActor(actor)
	record.Created = now
	record.Updated = now

	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.oidcBindings[record.Name]; ok && !old.Created.IsZero() {
		if old.Namespace != record.Namespace {
			return OIDCBindingResult{}, fmt.Errorf("oidc binding %q already exists in another namespace", record.Name)
		}
		record.Created = old.Created
	}
	s.oidcBindings[record.Name] = record
	s.appendAuditLocked(now, AuditEvent{
		Actor:      actor,
		Namespace:  record.Namespace,
		Action:     "oidc_binding.upsert",
		TargetType: "oidc_binding",
		TargetID:   record.Name,
		Fields: map[string]string{
			"issuer":   record.Issuer,
			"audience": record.Audience,
			"role":     record.Role,
			"jwks_url": record.JWKSURL,
		},
	})
	if err := s.persistLocked(); err != nil {
		return OIDCBindingResult{}, err
	}
	return OIDCBindingResult{Binding: publicOIDCBinding(record)}, nil
}

func (s *Store) DeleteOIDCBinding(name string) (OIDCBindingResult, error) {
	return s.DeleteOIDCBindingActor("controller", name)
}

func (s *Store) DeleteOIDCBindingActor(actor, name string) (OIDCBindingResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return OIDCBindingResult{}, fmt.Errorf("oidc binding name required")
	}
	now := s.now().UTC()
	actor = normalizeActor(actor)

	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.oidcBindings[name]
	if !ok {
		return OIDCBindingResult{}, fmt.Errorf("oidc binding %q not found", name)
	}
	delete(s.oidcBindings, name)
	s.appendAuditLocked(now, AuditEvent{
		Actor:      actor,
		Namespace:  record.Namespace,
		Action:     "oidc_binding.delete",
		TargetType: "oidc_binding",
		TargetID:   name,
	})
	if err := s.persistLocked(); err != nil {
		return OIDCBindingResult{}, err
	}
	return OIDCBindingResult{Binding: publicOIDCBinding(record)}, nil
}

func (s *Store) ListOIDCBindings() []OIDCBinding {
	return s.ListOIDCBindingsNamespace("")
}

func (s *Store) ListOIDCBindingsNamespace(namespace string) []OIDCBinding {
	namespace = normalizeNamespace(namespace)
	s.mu.Lock()
	defer s.mu.Unlock()
	records := s.sortedOIDCBindingsLocked()
	out := make([]OIDCBinding, 0, len(records))
	for _, record := range records {
		if !namespaceMatches(record.Namespace, namespace) {
			continue
		}
		out = append(out, publicOIDCBinding(record))
	}
	return out
}

func (s *Store) Get(id string) (HostRecord, bool) {
	id = strings.TrimSpace(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.hosts[id]
	if !ok {
		return HostRecord{}, false
	}
	return s.statusLocked(record), true
}

func (s *Store) List() []HostRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.sortedHostsLocked()
	for i := range out {
		out[i] = s.statusLocked(out[i])
	}
	return out
}

func (s *Store) reconcileLocked(now time.Time) ReconcileResult {
	var result ReconcileResult
	stale := make(map[string]bool)
	for id, host := range s.hosts {
		if !now.After(host.Expires) {
			continue
		}
		stale[id] = true
		if host.Status != "stale" {
			host.Status = "stale"
			s.hosts[id] = host
			result.StaleWorkers = append(result.StaleWorkers, id)
		}
	}

	for _, assignment := range s.sortedAssignmentsLocked() {
		if !reconcileAssignmentStatus(assignment.Status) {
			continue
		}
		leaseExpired := assignmentLeaseStatus(assignment.Status) && !assignment.LeaseExpires.IsZero() && now.After(assignment.LeaseExpires)
		leaseStale := assignment.LeasedTo != "" && stale[assignment.LeasedTo]
		workerStale := assignment.WorkerID != "" && stale[assignment.WorkerID]
		if !leaseExpired && !leaseStale && !workerStale {
			continue
		}

		changed := false
		if assignment.SandboxID != "" && assignment.SandboxRole == sandboxRoleRun {
			s.appendSandboxMeteringLocked(now, assignment)
		}
		if assignment.Status != "pending" {
			assignment.Status = "pending"
			changed = true
		}
		if assignment.LeasedTo != "" {
			assignment.LeasedTo = ""
			changed = true
		}
		if !assignment.LeaseExpires.IsZero() {
			assignment.LeaseExpires = time.Time{}
			changed = true
		}
		if workerStale && assignmentCanPlace(assignment) {
			selected, err := s.selectWorkerLocked(assignmentPolicy(assignment), assignment.ImageRef, assignment.RequiredLabels, assignment.AntiAffinityKey, assignment.Resources)
			if err == nil && selected != assignment.WorkerID {
				assignment.WorkerID = selected
				result.ReplacedAssignments = append(result.ReplacedAssignments, assignment.ID)
				changed = true
			}
		}
		if !changed {
			continue
		}
		assignment.Updated = now
		s.assignments[assignment.ID] = assignment
		result.RequeuedAssignments = append(result.RequeuedAssignments, assignment.ID)
	}
	for _, pool := range s.sortedWarmPoolsLocked() {
		downsized := s.downsizeWarmPoolLocked(now, pool)
		result.WarmPoolCanceled = append(result.WarmPoolCanceled, downsized.canceled...)
		for _, assignment := range downsized.cleanup {
			result.WarmPoolCleanup = append(result.WarmPoolCleanup, assignment.ID)
		}
		created := s.ensureWarmPoolLocked(now, pool)
		for _, assignment := range created {
			result.WarmPoolAssignments = append(result.WarmPoolAssignments, assignment.ID)
		}
	}
	sort.Strings(result.StaleWorkers)
	sort.Strings(result.RequeuedAssignments)
	sort.Strings(result.ReplacedAssignments)
	sort.Strings(result.WarmPoolAssignments)
	sort.Strings(result.WarmPoolCanceled)
	sort.Strings(result.WarmPoolCleanup)
	return result
}

func (s *Store) statusLocked(record HostRecord) HostRecord {
	if s.now().After(record.Expires) {
		record.Status = "stale"
	} else if record.Cordoned {
		record.Status = "cordoned"
	} else if record.Status == "" || record.Status == "stale" || record.Status == "cordoned" {
		record.Status = "ready"
	}
	record.Labels = cloneLabels(record.Labels)
	record.ImageRefs = cloneStrings(record.ImageRefs)
	if record.Report != nil {
		report := *record.Report
		record.Report = &report
	}
	return record
}

func (r ReconcileResult) changed() bool {
	return len(r.StaleWorkers) > 0 || len(r.RequeuedAssignments) > 0 || len(r.ReplacedAssignments) > 0 || len(r.WarmPoolAssignments) > 0 || len(r.WarmPoolCanceled) > 0 || len(r.WarmPoolCleanup) > 0
}

func activeAssignmentStatus(status string) bool {
	switch status {
	case "pending", "leased", "running", "ready":
		return true
	default:
		return false
	}
}

func reconcileAssignmentStatus(status string) bool {
	return activeAssignmentStatus(status) || status == "claimed"
}

func loadAssignmentStatus(status string) bool {
	return activeAssignmentStatus(status) || status == "claimed" || status == "draining"
}

func assignmentLeaseStatus(status string) bool {
	switch status {
	case "leased", "running", "ready", "claimed", "draining":
		return true
	default:
		return false
	}
}

func assignmentCanPlace(assignment Assignment) bool {
	if assignment.WarmPoolSlot != "" {
		return false
	}
	return assignment.Policy != "" || assignment.ImageRef != "" || len(assignment.RequiredLabels) > 0
}

func assignmentPolicy(assignment Assignment) string {
	if assignment.Policy == "" && assignment.ImageRef != "" {
		return PolicyImageAffinity
	}
	return assignment.Policy
}

func (s *Store) persistLocked() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return fmt.Errorf("create fleet store dir: %w", err)
	}
	hosts := make([]HostRecord, 0, len(s.hosts))
	for _, host := range s.hosts {
		hosts = append(hosts, host)
	}
	sort.Slice(hosts, func(i, j int) bool {
		return hosts[i].ID < hosts[j].ID
	})
	assignments := s.sortedAssignmentsLocked()
	warmPools := s.sortedWarmPoolsLocked()
	audit := cloneAuditEvents(s.audit)
	metering := s.sortedMeteringLocked()
	accounts := s.sortedServiceAccountsLocked()
	oidcBindings := s.sortedOIDCBindingsLocked()
	data, err := json.MarshalIndent(storeFile{Hosts: hosts, Assignments: assignments, WarmPools: warmPools, AuditEvents: audit, MeteringRecords: metering, ServiceAccounts: accounts, OIDCBindings: oidcBindings}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode fleet store: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0600); err != nil {
		return fmt.Errorf("write fleet store: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename fleet store: %w", err)
	}
	return nil
}

func (s *Store) sortedAssignmentsLocked() []Assignment {
	assignments := make([]Assignment, 0, len(s.assignments))
	for _, assignment := range s.assignments {
		assignments = append(assignments, assignment)
	}
	sort.Slice(assignments, func(i, j int) bool {
		if !assignments[i].Created.Equal(assignments[j].Created) {
			return assignments[i].Created.Before(assignments[j].Created)
		}
		return assignments[i].ID < assignments[j].ID
	})
	return assignments
}

func (s *Store) sortedHostsLocked() []HostRecord {
	hosts := make([]HostRecord, 0, len(s.hosts))
	for _, host := range s.hosts {
		hosts = append(hosts, host)
	}
	sort.Slice(hosts, func(i, j int) bool {
		return hosts[i].ID < hosts[j].ID
	})
	return hosts
}

func (s *Store) sortedWarmPoolsLocked() []WarmPool {
	pools := make([]WarmPool, 0, len(s.warmPools))
	for _, pool := range s.warmPools {
		pools = append(pools, cloneWarmPool(pool))
	}
	sort.Slice(pools, func(i, j int) bool {
		return pools[i].Name < pools[j].Name
	})
	return pools
}

func (s *Store) sortedAuditLocked() []AuditEvent {
	events := cloneAuditEvents(s.audit)
	sort.Slice(events, func(i, j int) bool {
		if !events[i].Time.Equal(events[j].Time) {
			return events[i].Time.Before(events[j].Time)
		}
		return events[i].ID < events[j].ID
	})
	return events
}

func (s *Store) sortedMeteringLocked() []SandboxMeteringRecord {
	records := cloneSandboxMeteringRecords(s.metering)
	sort.Slice(records, func(i, j int) bool {
		if !records[i].Time.Equal(records[j].Time) {
			return records[i].Time.Before(records[j].Time)
		}
		return records[i].ID < records[j].ID
	})
	return records
}

func (s *Store) sortedServiceAccountsLocked() []serviceAccountRecord {
	records := make([]serviceAccountRecord, 0, len(s.accounts))
	for _, record := range s.accounts {
		records = append(records, cloneServiceAccountRecord(record))
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].Name < records[j].Name
	})
	return records
}

func (s *Store) sortedOIDCBindingsLocked() []oidcBindingRecord {
	records := make([]oidcBindingRecord, 0, len(s.oidcBindings))
	for _, record := range s.oidcBindings {
		records = append(records, cloneOIDCBindingRecord(record))
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].Name < records[j].Name
	})
	return records
}

func (s *Store) nextAssignmentIDLocked(now time.Time) string {
	base := fmt.Sprintf("assignment-%d", now.UnixNano())
	id := base
	for i := 2; ; i++ {
		if _, ok := s.assignments[id]; !ok {
			return id
		}
		id = fmt.Sprintf("%s-%d", base, i)
	}
}

func (s *Store) nextAuditIDLocked(now time.Time) string {
	base := fmt.Sprintf("audit-%d", now.UnixNano())
	id := base
	for i := 2; ; i++ {
		found := false
		for _, event := range s.audit {
			if event.ID == id {
				found = true
				break
			}
		}
		if !found {
			return id
		}
		id = fmt.Sprintf("%s-%d", base, i)
	}
}

func (s *Store) nextMeteringIDLocked(now time.Time) string {
	base := fmt.Sprintf("metering-%d", now.UnixNano())
	id := base
	for i := 2; ; i++ {
		found := false
		for _, record := range s.metering {
			if record.ID == id {
				found = true
				break
			}
		}
		if !found {
			return id
		}
		id = fmt.Sprintf("%s-%d", base, i)
	}
}

func cloneAssignment(in Assignment) Assignment {
	out := in
	out.Args = cloneStrings(in.Args)
	out.RequiredLabels = cloneLabels(in.RequiredLabels)
	if in.LastReport != nil {
		report := *in.LastReport
		out.LastReport = &report
	}
	return out
}

func cloneAssignments(in []Assignment) []Assignment {
	if len(in) == 0 {
		return nil
	}
	out := make([]Assignment, len(in))
	for i := range in {
		out[i] = cloneAssignment(in[i])
	}
	return out
}

func cloneWarmPool(in WarmPool) WarmPool {
	out := in
	out.RequiredLabels = cloneLabels(in.RequiredLabels)
	out.Args = cloneStrings(in.Args)
	return out
}

func cloneAuditEvent(in AuditEvent) AuditEvent {
	out := in
	out.Fields = cloneLabels(in.Fields)
	return out
}

func cloneAuditEvents(in []AuditEvent) []AuditEvent {
	if len(in) == 0 {
		return nil
	}
	out := make([]AuditEvent, len(in))
	for i := range in {
		out[i] = cloneAuditEvent(in[i])
	}
	return out
}

func cloneSandboxMeteringRecord(in SandboxMeteringRecord) SandboxMeteringRecord {
	return in
}

func cloneSandboxMeteringRecords(in []SandboxMeteringRecord) []SandboxMeteringRecord {
	if len(in) == 0 {
		return nil
	}
	out := make([]SandboxMeteringRecord, len(in))
	for i := range in {
		out[i] = cloneSandboxMeteringRecord(in[i])
	}
	return out
}

func cloneServiceAccountRecord(in serviceAccountRecord) serviceAccountRecord {
	return in
}

func cloneOIDCBindingRecord(in oidcBindingRecord) oidcBindingRecord {
	in.Keys = cloneOIDCKeyRecords(in.Keys)
	return in
}

func cloneOIDCKeyRecords(in []oidcKeyRecord) []oidcKeyRecord {
	if len(in) == 0 {
		return nil
	}
	out := make([]oidcKeyRecord, len(in))
	copy(out, in)
	return out
}

func publicServiceAccount(record serviceAccountRecord) ServiceAccount {
	return ServiceAccount{
		Name:      record.Name,
		Namespace: record.Namespace,
		Role:      record.Role,
		Created:   record.Created,
		Updated:   record.Updated,
	}
}

func publicOIDCBinding(record oidcBindingRecord) OIDCBinding {
	return OIDCBinding{
		Name:        record.Name,
		Issuer:      record.Issuer,
		Subject:     record.Subject,
		Audience:    record.Audience,
		Namespace:   record.Namespace,
		Role:        record.Role,
		JWKSURL:     record.JWKSURL,
		JWKSFetched: record.JWKSFetched,
		KeyIDs:      oidcKeyIDs(record.Keys),
		Created:     record.Created,
		Updated:     record.Updated,
	}
}

func clonePlacementCandidates(in []PlacementCandidate) []PlacementCandidate {
	if len(in) == 0 {
		return nil
	}
	out := make([]PlacementCandidate, len(in))
	copy(out, in)
	return out
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func (s *Store) selectWorkerLocked(policy, imageRef string, labels map[string]string, antiAffinityKey string, resources Capacity) (string, error) {
	if policy == "" {
		policy = PolicyLeastLoaded
	}
	switch policy {
	case PolicyLeastLoaded, PolicyImageAffinity, PolicyBinPack:
	default:
		return "", fmt.Errorf("unknown assignment policy %q", policy)
	}
	candidates := s.placementCandidatesLocked(policy, imageRef, labels, antiAffinityKey, resources)
	if len(candidates) == 0 {
		return "", fmt.Errorf("no ready worker matches assignment")
	}
	return candidates[0].WorkerID, nil
}

func (s *Store) placementCandidatesLocked(policy, imageRef string, labels map[string]string, antiAffinityKey string, resources Capacity) []PlacementCandidate {
	if policy == "" {
		policy = PolicyLeastLoaded
	}
	resources = normalizeResources(resources)
	var candidates []PlacementCandidate
	for _, host := range s.sortedHostsLocked() {
		host = s.statusLocked(host)
		if host.Status != "ready" {
			continue
		}
		if !labelsMatch(host.Labels, labels) {
			continue
		}
		load := host.Capacity.VMs + s.pendingAssignmentsLocked(host.ID)
		if host.Capacity.MaxVMs > 0 && load+resources.VMs > host.Capacity.MaxVMs {
			continue
		}
		hasImage := imageRef != "" && containsString(host.ImageRefs, imageRef)
		antiAffinityLoad := s.antiAffinityLoadLocked(host.ID, antiAffinityKey)
		candidates = append(candidates, PlacementCandidate{
			WorkerID:         host.ID,
			Load:             load,
			MaxVMs:           host.Capacity.MaxVMs,
			RequestedVMs:     resources.VMs,
			AntiAffinityLoad: antiAffinityLoad,
			HasImage:         hasImage,
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		return betterCandidate(policy, candidates[i], candidates[j])
	})
	for i := range candidates {
		candidates[i].Rank = i + 1
	}
	return candidates
}

func betterCandidate(policy string, a, b PlacementCandidate) bool {
	if policy == PolicyImageAffinity && a.HasImage != b.HasImage {
		return a.HasImage
	}
	if a.AntiAffinityLoad != b.AntiAffinityLoad {
		return a.AntiAffinityLoad < b.AntiAffinityLoad
	}
	if policy == PolicyBinPack {
		if a.Load != b.Load {
			return a.Load > b.Load
		}
		if a.HasImage != b.HasImage {
			return a.HasImage
		}
		return a.WorkerID < b.WorkerID
	}
	if a.Load != b.Load {
		return a.Load < b.Load
	}
	return a.WorkerID < b.WorkerID
}

func (s *Store) ensureWarmPoolLocked(now time.Time, pool WarmPool) []Assignment {
	active := s.warmPoolAssignmentsLocked(pool.Name)
	need := pool.Size - len(active)
	if need <= 0 {
		return nil
	}
	var created []Assignment
	for i := 0; i < need; i++ {
		workerID, err := s.selectWorkerLocked(pool.Policy, pool.ImageRef, pool.RequiredLabels, warmPoolAntiAffinityKey(pool.Name), pool.Resources)
		if err != nil {
			return created
		}
		id := s.nextAssignmentIDLocked(now)
		assignment := Assignment{
			ID:              id,
			Namespace:       pool.Namespace,
			WorkerID:        workerID,
			WarmPool:        pool.Name,
			Policy:          pool.Policy,
			ImageRef:        pool.ImageRef,
			RequiredLabels:  cloneLabels(pool.RequiredLabels),
			AntiAffinityKey: warmPoolAntiAffinityKey(pool.Name),
			Resources:       pool.Resources,
			Verb:            "cove",
			Args:            warmPoolArgs(pool, id),
			Status:          "pending",
			Created:         now,
			Updated:         now,
		}
		s.assignments[id] = assignment
		created = append(created, cloneAssignment(assignment))
	}
	return created
}

type warmPoolDownsizeResult struct {
	canceled []string
	cleanup  []Assignment
	deferred []string
}

func (s *Store) downsizeWarmPoolLocked(now time.Time, pool WarmPool) warmPoolDownsizeResult {
	active := s.warmPoolAssignmentsLocked(pool.Name)
	extra := len(active) - pool.Size
	if extra <= 0 {
		return warmPoolDownsizeResult{}
	}
	sort.Slice(active, func(i, j int) bool {
		a, b := active[i], active[j]
		if ra, rb := warmPoolDownsizeRank(a.Status), warmPoolDownsizeRank(b.Status); ra != rb {
			return ra < rb
		}
		if !a.Created.Equal(b.Created) {
			return a.Created.After(b.Created)
		}
		return a.ID > b.ID
	})
	var result warmPoolDownsizeResult
	for _, assignment := range active {
		if extra == 0 {
			break
		}
		s.retireWarmPoolSlotLocked(now, assignment, &result)
		extra--
	}
	sort.Strings(result.canceled)
	return result
}

func (s *Store) retireWarmPoolLocked(now time.Time, name string) warmPoolDownsizeResult {
	var slots []Assignment
	for _, assignment := range s.sortedAssignmentsLocked() {
		if assignment.WarmPool != name {
			continue
		}
		switch assignment.Status {
		case "pending", "leased", "running", "ready", "draining", "claimed":
			slots = append(slots, assignment)
		}
	}
	sort.Slice(slots, func(i, j int) bool {
		a, b := slots[i], slots[j]
		if ra, rb := warmPoolDownsizeRank(a.Status), warmPoolDownsizeRank(b.Status); ra != rb {
			return ra < rb
		}
		if !a.Created.Equal(b.Created) {
			return a.Created.After(b.Created)
		}
		return a.ID > b.ID
	})
	var result warmPoolDownsizeResult
	for _, assignment := range slots {
		s.retireWarmPoolSlotLocked(now, assignment, &result)
	}
	sort.Strings(result.canceled)
	sort.Strings(result.deferred)
	return result
}

func (s *Store) retireWarmPoolSlotLocked(now time.Time, assignment Assignment, result *warmPoolDownsizeResult) {
	if assignment.Status == "pending" || strings.TrimSpace(assignment.WorkerID) == "" {
		assignment.Status = "canceled"
		assignment.Updated = now
		s.assignments[assignment.ID] = assignment
		result.canceled = append(result.canceled, assignment.ID)
		return
	}
	if assignment.Status == "claimed" && s.activeWarmPoolClaimLocked(assignment.ID) {
		result.deferred = append(result.deferred, assignment.ID)
		return
	}
	if s.activeWarmPoolCleanupLocked(assignment.ID) {
		if assignment.Status != "draining" {
			assignment.Status = "draining"
			assignment.Updated = now
			s.assignments[assignment.ID] = assignment
		}
		return
	}
	vmName := WarmPoolAssignmentVMName(assignment)
	cleanup := Assignment{
		ID:           s.nextAssignmentIDLocked(now),
		Namespace:    assignment.Namespace,
		WorkerID:     assignment.WorkerID,
		WarmPoolSlot: assignment.ID,
		Verb:         "cove",
		Args:         warmPoolStopArgs(vmName),
		Status:       "pending",
		Created:      now,
		Updated:      now,
	}
	assignment.Status = "draining"
	assignment.Updated = now
	s.assignments[assignment.ID] = assignment
	s.assignments[cleanup.ID] = cleanup
	result.cleanup = append(result.cleanup, cloneAssignment(cleanup))
}

func warmPoolDownsizeRank(status string) int {
	switch status {
	case "pending":
		return 0
	case "ready":
		return 1
	case "running":
		return 2
	case "leased":
		return 3
	case "draining":
		return 4
	case "claimed":
		return 5
	default:
		return 6
	}
}

func (s *Store) warmPoolStatusLocked(name string) WarmPoolStatus {
	pool, ok := s.warmPools[name]
	if !ok {
		return WarmPoolStatus{}
	}
	assignments := s.warmPoolAssignmentsLocked(name)
	return WarmPoolStatus{
		WarmPool:    cloneWarmPool(pool),
		Active:      len(assignments),
		Assignments: cloneAssignments(assignments),
	}
}

func (s *Store) warmPoolAssignmentsLocked(name string) []Assignment {
	var assignments []Assignment
	for _, assignment := range s.sortedAssignmentsLocked() {
		if assignment.WarmPool != name || !activeAssignmentStatus(assignment.Status) {
			continue
		}
		assignments = append(assignments, cloneAssignment(assignment))
	}
	return assignments
}

func (s *Store) claimableWarmPoolSlotLocked(name string) (Assignment, bool) {
	for _, assignment := range s.sortedAssignmentsLocked() {
		if assignment.WarmPool == name && assignment.WorkerID != "" && assignment.Status == "ready" {
			return assignment, true
		}
	}
	return Assignment{}, false
}

func (s *Store) sandboxRunAssignmentLocked(id string) (Assignment, bool) {
	id = strings.TrimSpace(id)
	for _, assignment := range s.sortedAssignmentsLocked() {
		if assignment.SandboxID == id && assignment.SandboxRole == sandboxRoleRun {
			return cloneAssignment(assignment), true
		}
	}
	return Assignment{}, false
}

func (s *Store) activeSandboxCleanupLocked(id string) (Assignment, bool) {
	id = strings.TrimSpace(id)
	for _, assignment := range s.sortedAssignmentsLocked() {
		if assignment.SandboxID != id || assignment.SandboxRole != sandboxRoleStop {
			continue
		}
		if activeAssignmentStatus(assignment.Status) {
			return cloneAssignment(assignment), true
		}
	}
	return Assignment{}, false
}

func (s *Store) startSandboxLocked(now time.Time, actor, id, action string) (SandboxStartResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return SandboxStartResult{}, fmt.Errorf("sandbox id required")
	}
	s.reconcileLocked(now)
	assignment, ok := s.sandboxRunAssignmentLocked(id)
	if !ok {
		return SandboxStartResult{}, fmt.Errorf("sandbox %q not found", id)
	}
	vmName := SandboxAssignmentVMName(assignment)
	result := SandboxStartResult{
		Namespace:  assignment.Namespace,
		ID:         id,
		VMName:     vmName,
		Status:     assignment.Status,
		Assignment: cloneAssignment(assignment),
	}
	if !sandboxTerminalStatus(assignment.Status) {
		return result, nil
	}
	if assignment.Status == "stopped" || assignment.Status == "complete" || sandboxRunUsesExistingVM(assignment.Args) {
		if err := s.requireSandboxWorkerReadyLocked(now, assignment.WorkerID); err != nil {
			return SandboxStartResult{}, err
		}
		assignment.Args = sandboxStartArgs(assignment)
	} else if assignmentCanPlace(assignment) {
		workerID, err := s.selectWorkerLocked(assignmentPolicy(assignment), assignment.ImageRef, assignment.RequiredLabels, assignment.AntiAffinityKey, assignment.Resources)
		if err != nil {
			return SandboxStartResult{}, err
		}
		assignment.WorkerID = workerID
	} else if err := s.requireSandboxWorkerReadyLocked(now, assignment.WorkerID); err != nil {
		return SandboxStartResult{}, err
	}
	assignment.Status = "pending"
	assignment.LeasedTo = ""
	assignment.LeaseExpires = time.Time{}
	assignment.LastReport = nil
	assignment.Updated = now
	s.assignments[assignment.ID] = assignment
	result.Status = assignment.Status
	result.Started = true
	result.Assignment = cloneAssignment(assignment)
	s.appendAuditLocked(now, AuditEvent{
		Actor:        actor,
		Namespace:    assignment.Namespace,
		Action:       action,
		TargetType:   "sandbox",
		TargetID:     id,
		WorkerID:     assignment.WorkerID,
		AssignmentID: assignment.ID,
		Fields: map[string]string{
			"vm_name": vmName,
			"status":  assignment.Status,
		},
	})
	return result, nil
}

func (s *Store) requireSandboxWorkerReadyLocked(now time.Time, workerID string) error {
	workerID = strings.TrimSpace(workerID)
	if workerID == "" {
		return fmt.Errorf("sandbox worker required")
	}
	record, ok := s.hosts[workerID]
	if !ok {
		return fmt.Errorf("sandbox worker %q not registered", workerID)
	}
	status := s.statusLocked(record)
	if status.Status != "ready" {
		return fmt.Errorf("sandbox worker %q is %s", workerID, status.Status)
	}
	if now.After(status.Expires) {
		return fmt.Errorf("sandbox worker %q is stale", workerID)
	}
	return nil
}

func (s *Store) stopSandboxLocked(now time.Time, actor, id, action string) (SandboxStopResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return SandboxStopResult{}, fmt.Errorf("sandbox id required")
	}
	s.reconcileLocked(now)
	assignment, ok := s.sandboxRunAssignmentLocked(id)
	if !ok {
		return SandboxStopResult{}, fmt.Errorf("sandbox %q not found", id)
	}
	vmName := SandboxAssignmentVMName(assignment)
	result := SandboxStopResult{
		Namespace:  assignment.Namespace,
		ID:         id,
		VMName:     vmName,
		Status:     assignment.Status,
		Assignment: cloneAssignment(assignment),
	}
	switch assignment.Status {
	case "pending":
		assignment.Status = "canceled"
		assignment.Updated = now
		s.assignments[assignment.ID] = assignment
		result.Status = assignment.Status
		result.Canceled = true
		result.Assignment = cloneAssignment(assignment)
	case "leased", "running", "ready", "draining", "restarting":
		cleanup, ok := s.activeSandboxCleanupLocked(id)
		if !ok {
			cleanup = Assignment{
				ID:          s.nextAssignmentIDLocked(now),
				Namespace:   assignment.Namespace,
				WorkerID:    assignment.WorkerID,
				SandboxID:   id,
				SandboxRole: sandboxRoleStop,
				Verb:        "cove",
				Args:        sandboxStopArgs(vmName),
				Status:      "pending",
				Created:     now,
				Updated:     now,
			}
			s.assignments[cleanup.ID] = cleanup
		}
		s.appendSandboxMeteringLocked(now, assignment)
		assignment.Status = "draining"
		assignment.Updated = now
		s.assignments[assignment.ID] = assignment
		result.Status = assignment.Status
		result.Assignment = cloneAssignment(assignment)
		cleanup = cloneAssignment(cleanup)
		result.Cleanup = &cleanup
	}
	s.appendAuditLocked(now, AuditEvent{
		Actor:        actor,
		Namespace:    assignment.Namespace,
		Action:       action,
		TargetType:   "sandbox",
		TargetID:     id,
		WorkerID:     assignment.WorkerID,
		AssignmentID: assignment.ID,
		Fields: map[string]string{
			"vm_name":  vmName,
			"status":   result.Status,
			"canceled": strconv.FormatBool(result.Canceled),
			"cleanup":  strconv.FormatBool(result.Cleanup != nil),
		},
	})
	return result, nil
}

func (s *Store) finishSandboxStopLocked(now time.Time, id, stopStatus string) {
	run, ok := s.sandboxRunAssignmentLocked(id)
	if !ok || !sandboxPendingStopStatus(run.Status) {
		return
	}
	if stopStatus == "complete" {
		if run.Status == "restarting" {
			run.Args = sandboxStartArgs(run)
			run.Status = "pending"
			run.LeasedTo = ""
			run.LeaseExpires = time.Time{}
			run.LastReport = nil
		} else {
			run.Status = "stopped"
		}
	} else if !assignmentLeaseStatus(stopStatus) {
		run.Status = "failed"
	}
	run.Updated = now
	s.assignments[run.ID] = run
}

func (s *Store) nextSandboxIDLocked(now time.Time) string {
	base := fmt.Sprintf("sandbox-%d", now.UnixNano())
	id := base
	for i := 2; ; i++ {
		if _, ok := s.sandboxRunAssignmentLocked(id); !ok {
			if _, exists := s.assignments[id]; !exists {
				return id
			}
		}
		id = fmt.Sprintf("%s-%d", base, i)
	}
}

func warmPoolFromRequest(req WarmPoolRequest, now time.Time) (WarmPool, error) {
	pool := WarmPool{
		Namespace:      normalizeNamespace(req.Namespace),
		Name:           strings.TrimSpace(req.Name),
		ImageRef:       strings.TrimSpace(req.ImageRef),
		Size:           req.Size,
		Policy:         strings.TrimSpace(req.Policy),
		RequiredLabels: cloneLabels(req.RequiredLabels),
		Resources:      req.Resources,
		Args:           cloneStrings(req.Args),
		Created:        now,
		Updated:        now,
	}
	if pool.Name == "" {
		pool.Name = warmPoolDefaultName(pool.ImageRef)
	}
	return normalizeWarmPool(pool, now)
}

func normalizeWarmPool(pool WarmPool, now time.Time) (WarmPool, error) {
	pool.Name = strings.TrimSpace(pool.Name)
	pool.Namespace = normalizeNamespace(pool.Namespace)
	pool.ImageRef = strings.TrimSpace(pool.ImageRef)
	pool.Policy = strings.TrimSpace(pool.Policy)
	pool.RequiredLabels = cloneLabels(pool.RequiredLabels)
	pool.Args = cloneStrings(pool.Args)
	if err := validateForkRunArgs(pool.Args, "warm pool"); err != nil {
		return WarmPool{}, err
	}
	resources, err := sanitizeResources(pool.Resources)
	if err != nil {
		return WarmPool{}, err
	}
	pool.Resources = normalizeResources(resources)
	if pool.Name == "" {
		return WarmPool{}, fmt.Errorf("warm pool name required")
	}
	if pool.ImageRef == "" {
		return WarmPool{}, fmt.Errorf("warm pool image_ref required")
	}
	if pool.Size < 0 {
		return WarmPool{}, fmt.Errorf("warm pool size must not be negative")
	}
	if pool.Policy == "" {
		pool.Policy = PolicyImageAffinity
	}
	switch pool.Policy {
	case PolicyLeastLoaded, PolicyImageAffinity, PolicyBinPack:
	default:
		return WarmPool{}, fmt.Errorf("unknown warm pool policy %q", pool.Policy)
	}
	if pool.Created.IsZero() && !now.IsZero() {
		pool.Created = now
	}
	if pool.Updated.IsZero() && !now.IsZero() {
		pool.Updated = now
	}
	return pool, nil
}

func warmPoolArgs(pool WarmPool, assignmentID string) []string {
	args := []string{
		"run",
		"-fork-from", pool.ImageRef,
		"-fork-name", warmPoolForkName(pool.Name, assignmentID),
		"-ephemeral",
		"-keep",
		"-headless",
	}
	return append(args, cloneStrings(pool.Args)...)
}

func WarmPoolAssignmentVMName(assignment Assignment) string {
	return assignmentForkName(assignment, warmPoolForkName(assignment.WarmPool, assignment.ID))
}

func SandboxAssignmentVMName(assignment Assignment) string {
	return assignmentForkName(assignment, sandboxVMName(assignment.SandboxID))
}

func assignmentForkName(assignment Assignment, fallback string) string {
	for i, arg := range assignment.Args {
		if (arg == "-fork-name" || arg == "--fork-name") && i+1 < len(assignment.Args) {
			name := strings.TrimSpace(assignment.Args[i+1])
			if name != "" {
				return name
			}
		}
		for _, prefix := range []string{"-fork-name=", "--fork-name="} {
			if strings.HasPrefix(arg, prefix) {
				name := strings.TrimSpace(strings.TrimPrefix(arg, prefix))
				if name != "" {
					return name
				}
			}
		}
	}
	return fallback
}

func sandboxStatusFromAssignment(assignment Assignment, now time.Time) SandboxStatus {
	assignment = cloneAssignment(assignment)
	clearExpiredSandboxLease(&assignment, now)
	status := SandboxStatus{
		Namespace:  assignment.Namespace,
		ID:         assignment.SandboxID,
		VMName:     SandboxAssignmentVMName(assignment),
		ImageRef:   assignment.ImageRef,
		WorkerID:   assignment.WorkerID,
		Status:     assignment.Status,
		Assignment: assignment,
		Created:    assignment.Created,
		Updated:    assignment.Updated,
	}
	if assignment.SandboxLeaseHolder != "" && !assignment.SandboxLeaseExpires.IsZero() {
		status.Lease = &SandboxLease{
			Holder:  assignment.SandboxLeaseHolder,
			Expires: assignment.SandboxLeaseExpires,
		}
	}
	return status
}

func sandboxLeaseRequest(req SandboxLeaseRequest, actor string, now time.Time) (string, time.Time, error) {
	holder := normalizeSandboxLeaseHolder(req.Holder, actor)
	ttl := DefaultSandboxLeaseTTL
	if raw := strings.TrimSpace(req.TTL); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			return "", time.Time{}, fmt.Errorf("sandbox lease ttl must be a positive duration")
		}
		ttl = parsed
	}
	return holder, now.Add(ttl), nil
}

func normalizeSandboxLeaseHolder(holder, actor string) string {
	holder = strings.TrimSpace(holder)
	if holder != "" {
		return holder
	}
	return normalizeActor(actor)
}

func clearExpiredSandboxLease(assignment *Assignment, now time.Time) bool {
	if assignment == nil || assignment.SandboxLeaseHolder == "" || assignment.SandboxLeaseExpires.IsZero() || now.IsZero() {
		return false
	}
	if !now.Before(assignment.SandboxLeaseExpires) {
		assignment.SandboxLeaseHolder = ""
		assignment.SandboxLeaseExpires = time.Time{}
		return true
	}
	return false
}

func sandboxRunArgs(imageRef, vmName string, extra []string) []string {
	args := []string{
		"run",
		"-fork-from", imageRef,
		"-fork-name", vmName,
		"-ephemeral",
		"-keep",
		"-headless",
	}
	return append(args, cloneStrings(extra)...)
}

func sandboxStartArgs(assignment Assignment) []string {
	vmName := SandboxAssignmentVMName(assignment)
	args := []string{"run", "-vm", vmName, "-headless"}
	return append(args, sandboxRunExtraArgs(assignment.Args)...)
}

func sandboxRunExtraArgs(args []string) []string {
	args = cloneStrings(args)
	if len(args) >= 8 && args[0] == "run" && args[1] == "-fork-from" && args[3] == "-fork-name" && args[5] == "-ephemeral" && args[6] == "-keep" && args[7] == "-headless" {
		return cloneStrings(args[8:])
	}
	if sandboxRunUsesExistingVM(args) {
		return cloneStrings(args[4:])
	}
	return nil
}

func sandboxRunUsesExistingVM(args []string) bool {
	return len(args) >= 4 && args[0] == "run" && args[1] == "-vm" && args[3] == "-headless"
}

func sandboxStopArgs(vmName string) []string {
	return []string{"ctl", "-vm", vmName, "stop"}
}

func sandboxTerminalStatus(status string) bool {
	return !activeAssignmentStatus(status) && !sandboxPendingStopStatus(status)
}

func sandboxPendingStopStatus(status string) bool {
	return status == "draining" || status == "restarting"
}

func sandboxVMName(id string) string {
	return "cove-sandbox-" + warmPoolSafeName(id)
}

func warmPoolClaimArgs(vmName string, command []string, env map[string]string) []string {
	args := []string{"shell"}
	if len(env) > 0 {
		keys := make([]string, 0, len(env))
		for key := range env {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			args = append(args, "--env", key+"="+env[key])
		}
	}
	args = append(args, vmName, "--")
	return append(args, cloneStrings(command)...)
}

func sandboxExecArgs(vmName string, command []string, env map[string]string) []string {
	return warmPoolClaimArgs(vmName, command, env)
}

func sandboxControlPayload(req SandboxControlRequest) ([]byte, string, error) {
	typ := strings.TrimSpace(req.Type)
	var body map[string]any
	switch typ {
	case "screenshot":
		body = req.Screenshot
		if body == nil {
			body = map[string]any{}
		}
	case "key":
		body = req.Key
	case "mouse":
		body = req.Mouse
	case "text":
		body = req.Text
	default:
		return nil, "", fmt.Errorf("sandbox control type must be screenshot, key, mouse, or text")
	}
	if body == nil {
		return nil, "", fmt.Errorf("sandbox control %s payload required", typ)
	}
	payload, err := json.Marshal(map[string]any{
		"type": typ,
		typ:    body,
	})
	if err != nil {
		return nil, "", fmt.Errorf("encode sandbox control payload: %w", err)
	}
	return payload, typ, nil
}

func warmPoolStopArgs(vmName string) []string {
	return []string{"ctl", "-vm", vmName, "stop"}
}

func validateForkRunArgs(args []string, label string) error {
	label = strings.TrimSpace(label)
	if label == "" {
		label = "fork run"
	}
	reserved := map[string]bool{
		"-fork-from":  true,
		"--fork-from": true,
		"-fork-name":  true,
		"--fork-name": true,
		"-ephemeral":  true,
		"--ephemeral": true,
		"-keep":       true,
		"--keep":      true,
		"-headless":   true,
		"--headless":  true,
	}
	for _, arg := range args {
		flag := arg
		if i := strings.IndexByte(flag, '='); i >= 0 {
			flag = flag[:i]
		}
		if reserved[flag] {
			return fmt.Errorf("%s args must not set %s", label, flag)
		}
	}
	return nil
}

func warmPoolAntiAffinityKey(name string) string {
	return "warm-pool/" + name
}

func warmPoolDefaultName(imageRef string) string {
	return warmPoolSafeName(imageRef)
}

func warmPoolForkName(poolName, assignmentID string) string {
	return "cove-warm-" + warmPoolSafeName(poolName) + "-" + warmPoolSafeName(assignmentID)
}

func warmPoolSafeName(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	dash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "pool"
	}
	return strings.ToLower(out)
}

func (s *Store) antiAffinityLoadLocked(workerID, key string) int {
	key = strings.TrimSpace(key)
	if key == "" {
		return 0
	}
	var n int
	for _, assignment := range s.assignments {
		if assignment.AntiAffinityKey != key || !loadAssignmentStatus(assignment.Status) {
			continue
		}
		if assignment.WorkerID == workerID || assignment.LeasedTo == workerID {
			n += assignmentVMs(assignment)
		}
	}
	return n
}

func (s *Store) pendingAssignmentsLocked(workerID string) int {
	var n int
	for _, assignment := range s.assignments {
		if assignment.WorkerID != workerID && assignment.LeasedTo != workerID {
			continue
		}
		if loadAssignmentStatus(assignment.Status) {
			n += assignmentVMs(assignment)
		}
	}
	return n
}

func normalizeResources(in Capacity) Capacity {
	if in.VMs <= 0 {
		in.VMs = 1
	}
	return in
}

func sanitizeResources(in Capacity) (Capacity, error) {
	if in.CPUs < 0 || in.VMs < 0 || in.MaxVMs < 0 || in.Images < 0 {
		return Capacity{}, fmt.Errorf("assignment resources must not be negative")
	}
	return in, nil
}

func normalizePlacementFields(a Assignment) (policy, imageRef, antiAffinityKey string, labels map[string]string, resources Capacity, err error) {
	policy = strings.TrimSpace(a.Policy)
	imageRef = strings.TrimSpace(a.ImageRef)
	antiAffinityKey = strings.TrimSpace(a.AntiAffinityKey)
	labels = cloneLabels(a.RequiredLabels)
	resources, err = sanitizeResources(a.Resources)
	if err != nil {
		return "", "", "", nil, Capacity{}, err
	}
	if policy == "" && imageRef != "" {
		policy = PolicyImageAffinity
	}
	switch policy {
	case "", PolicyLeastLoaded, PolicyImageAffinity, PolicyBinPack:
	default:
		return "", "", "", nil, Capacity{}, fmt.Errorf("unknown assignment policy %q", policy)
	}
	return policy, imageRef, antiAffinityKey, labels, resources, nil
}

func assignmentVMs(assignment Assignment) int {
	if assignment.WarmPoolSlot != "" || assignment.SandboxRole == sandboxRoleStop {
		return 0
	}
	return normalizeResources(assignment.Resources).VMs
}

func (s *Store) activeImagePrepareLocked(workerID, imageRef string) bool {
	for _, assignment := range s.assignments {
		if assignment.WorkerID != workerID || assignment.ImageRef != imageRef {
			continue
		}
		if assignment.Verb == "cove" && len(assignment.Args) >= 2 && assignment.Args[0] == "image" && assignment.Args[1] == "pull" && activeAssignmentStatus(assignment.Status) {
			return true
		}
	}
	return false
}

func (s *Store) activeImageGCLocked(workerID string) bool {
	for _, assignment := range s.assignments {
		if assignment.WorkerID != workerID {
			continue
		}
		if assignment.Verb == "cove" && len(assignment.Args) >= 2 && assignment.Args[0] == "image" && assignment.Args[1] == "gc" && activeAssignmentStatus(assignment.Status) {
			return true
		}
	}
	return false
}

func (s *Store) activeLifecyclePolicyLocked(workerID, vmName string) bool {
	for _, assignment := range s.assignments {
		if assignment.WorkerID != workerID {
			continue
		}
		if assignment.Verb == "cove" && len(assignment.Args) >= 2 && assignment.Args[0] == "policy" && assignment.Args[1] == vmName && activeAssignmentStatus(assignment.Status) {
			return true
		}
	}
	return false
}

func (s *Store) activeStorageBudgetLocked(workerID string) bool {
	for _, assignment := range s.assignments {
		if assignment.WorkerID != workerID {
			continue
		}
		if assignment.Verb == "cove" && len(assignment.Args) >= 2 && assignment.Args[0] == "storage" && assignment.Args[1] == "budget" && activeAssignmentStatus(assignment.Status) {
			return true
		}
	}
	return false
}

func (s *Store) activeStoragePruneLocked(workerID string) bool {
	for _, assignment := range s.assignments {
		if assignment.WorkerID != workerID {
			continue
		}
		if assignment.Verb == "cove" && len(assignment.Args) >= 2 && assignment.Args[0] == "storage" && assignment.Args[1] == "prune" && activeAssignmentStatus(assignment.Status) {
			return true
		}
	}
	return false
}

func (s *Store) activeWarmPoolClaimLocked(slotID string) bool {
	for _, assignment := range s.assignments {
		if assignment.WarmPoolSlot != slotID || !activeAssignmentStatus(assignment.Status) {
			continue
		}
		if assignment.Verb == "cove" && len(assignment.Args) > 0 && assignment.Args[0] == "shell" {
			return true
		}
	}
	return false
}

func (s *Store) activeWarmPoolCleanupLocked(slotID string) bool {
	for _, assignment := range s.assignments {
		if assignment.WarmPoolSlot != slotID || !activeAssignmentStatus(assignment.Status) {
			continue
		}
		if assignment.Verb == "cove" && len(assignment.Args) >= 4 && assignment.Args[0] == "ctl" && assignment.Args[1] == "-vm" && assignment.Args[3] == "stop" {
			return true
		}
	}
	return false
}

func (s *Store) appendSandboxMeteringLocked(ended time.Time, assignment Assignment) {
	if assignment.SandboxID == "" || assignment.SandboxRole != sandboxRoleRun || !sandboxMeteredStatus(assignment.Status) {
		return
	}
	if assignment.Updated.IsZero() || !ended.After(assignment.Updated) {
		return
	}
	durationMillis := ended.Sub(assignment.Updated).Milliseconds()
	if durationMillis <= 0 {
		return
	}
	resources := normalizeResources(assignment.Resources)
	record := SandboxMeteringRecord{
		ID:             s.nextMeteringIDLocked(ended),
		Time:           ended.UTC(),
		Namespace:      assignment.Namespace,
		SandboxID:      assignment.SandboxID,
		AssignmentID:   assignment.ID,
		WorkerID:       assignment.WorkerID,
		Status:         assignment.Status,
		Started:        assignment.Updated.UTC(),
		Ended:          ended.UTC(),
		DurationMillis: durationMillis,
		Resources:      resources,
		VMMillis:       durationMillis * int64(resources.VMs),
	}
	if resources.CPUs > 0 {
		record.CPUMillis = durationMillis * int64(resources.CPUs)
	}
	if resources.MemoryBytes > 0 {
		record.MemoryByteMillis = saturatingMulUint64(uint64(durationMillis), resources.MemoryBytes)
	}
	s.metering = append(s.metering, normalizeSandboxMeteringRecord(record))
}

func (s *Store) appendAuditLocked(now time.Time, event AuditEvent) AuditEvent {
	event = normalizeAuditEvent(event)
	if event.Time.IsZero() {
		event.Time = now
	}
	if event.ID == "" {
		event.ID = s.nextAuditIDLocked(event.Time)
	}
	if event.Actor == "" {
		event.Actor = "controller"
	}
	event.PrevHash = s.lastAuditHashLocked()
	event.Hash = auditEventHash(event)
	s.audit = append(s.audit, cloneAuditEvent(event))
	return event
}

func normalizeAuditEvent(event AuditEvent) AuditEvent {
	event.ID = strings.TrimSpace(event.ID)
	event.Namespace = normalizeNamespace(event.Namespace)
	event.Actor = strings.TrimSpace(event.Actor)
	event.Action = strings.TrimSpace(event.Action)
	event.TargetType = strings.TrimSpace(event.TargetType)
	event.TargetID = strings.TrimSpace(event.TargetID)
	event.WorkerID = strings.TrimSpace(event.WorkerID)
	event.AssignmentID = strings.TrimSpace(event.AssignmentID)
	event.Status = strings.TrimSpace(event.Status)
	event.Fields = cloneLabels(event.Fields)
	event.PrevHash = strings.TrimSpace(event.PrevHash)
	event.Hash = strings.TrimSpace(event.Hash)
	if !event.Time.IsZero() {
		event.Time = event.Time.UTC()
	}
	return event
}

func normalizeSandboxMeteringRecord(record SandboxMeteringRecord) SandboxMeteringRecord {
	record.ID = strings.TrimSpace(record.ID)
	record.Namespace = normalizeNamespace(record.Namespace)
	record.SandboxID = strings.TrimSpace(record.SandboxID)
	record.AssignmentID = strings.TrimSpace(record.AssignmentID)
	record.WorkerID = strings.TrimSpace(record.WorkerID)
	record.Status = strings.TrimSpace(record.Status)
	record.Resources = normalizeResources(record.Resources)
	if !record.Time.IsZero() {
		record.Time = record.Time.UTC()
	}
	if !record.Started.IsZero() {
		record.Started = record.Started.UTC()
	}
	if !record.Ended.IsZero() {
		record.Ended = record.Ended.UTC()
	}
	if record.DurationMillis < 0 {
		record.DurationMillis = 0
	}
	if record.VMMillis < 0 {
		record.VMMillis = 0
	}
	if record.CPUMillis < 0 {
		record.CPUMillis = 0
	}
	return record
}

func sandboxMeteringSummary(namespace, sandboxID string, records []SandboxMeteringRecord) SandboxMeteringSummary {
	summary := SandboxMeteringSummary{
		Namespace: normalizeNamespace(namespace),
		SandboxID: strings.TrimSpace(sandboxID),
		Records:   len(records),
	}
	for _, record := range records {
		summary.DurationMillis += record.DurationMillis
		summary.VMMillis += record.VMMillis
		summary.CPUMillis += record.CPUMillis
		summary.MemoryByteMillis = saturatingAddUint64(summary.MemoryByteMillis, record.MemoryByteMillis)
	}
	return summary
}

func sandboxExecResult(id, vmName string, assignment Assignment) SandboxExecResult {
	result := SandboxExecResult{
		Namespace:  assignment.Namespace,
		ID:         strings.TrimSpace(id),
		VMName:     strings.TrimSpace(vmName),
		Done:       !activeAssignmentStatus(assignment.Status),
		Assignment: cloneAssignment(assignment),
	}
	if assignment.LastReport != nil {
		result.ExitCode = assignment.LastReport.ExitCode
		result.Stdout = assignment.LastReport.Stdout
		result.Stderr = assignment.LastReport.Stderr
		result.Error = assignment.LastReport.Error
	}
	return result
}

func sandboxControlResult(id, vmName, typ string, assignment Assignment) SandboxControlResult {
	result := SandboxControlResult{
		Namespace:  assignment.Namespace,
		ID:         strings.TrimSpace(id),
		VMName:     strings.TrimSpace(vmName),
		Type:       strings.TrimSpace(typ),
		Done:       !activeAssignmentStatus(assignment.Status),
		Assignment: cloneAssignment(assignment),
	}
	if assignment.LastReport == nil {
		return result
	}
	result.ExitCode = assignment.LastReport.ExitCode
	result.Stdout = assignment.LastReport.Stdout
	result.Stderr = assignment.LastReport.Stderr
	result.Error = assignment.LastReport.Error
	var response map[string]any
	if err := json.Unmarshal([]byte(assignment.LastReport.Stdout), &response); err == nil {
		result.Response = response
		if data, ok := response["data"].(string); ok {
			result.Data = data
		}
		if result.Data == "" {
			if screenshot, ok := response["screenshot_result"].(map[string]any); ok {
				if data, ok := screenshot["image_data"].(string); ok {
					result.Data = data
				}
			}
		}
		if success, ok := response["success"].(bool); ok && !success && result.Error == "" {
			if msg, ok := response["error"].(string); ok {
				result.Error = msg
			}
		}
	}
	return result
}

func sandboxMeteredStatus(status string) bool {
	return status == "running" || status == "ready"
}

func saturatingMulUint64(a, b uint64) uint64 {
	if a == 0 || b == 0 {
		return 0
	}
	max := ^uint64(0)
	if a > max/b {
		return max
	}
	return a * b
}

func saturatingAddUint64(a, b uint64) uint64 {
	max := ^uint64(0)
	if max-a < b {
		return max
	}
	return a + b
}

func (s *Store) lastAuditHashLocked() string {
	if len(s.audit) == 0 {
		return ""
	}
	last := normalizeAuditEvent(s.audit[len(s.audit)-1])
	if last.Hash != "" {
		return last.Hash
	}
	return auditEventHash(last)
}

func chainLegacyAuditEvents(events []AuditEvent) []AuditEvent {
	events = cloneAuditEvents(events)
	prev := ""
	for i := range events {
		events[i] = normalizeAuditEvent(events[i])
		if events[i].PrevHash == "" && events[i].Hash == "" {
			events[i].PrevHash = prev
			events[i].Hash = auditEventHash(events[i])
		}
		if events[i].Hash != "" {
			prev = events[i].Hash
		} else {
			prev = auditEventHash(events[i])
		}
	}
	return events
}

func verifyAuditEvents(events []AuditEvent) AuditVerifyResult {
	result := AuditVerifyResult{
		OK:     true,
		Events: len(events),
	}
	wantPrev := ""
	for i, event := range events {
		event = normalizeAuditEvent(event)
		if event.PrevHash != wantPrev {
			result.OK = false
			result.Issues = append(result.Issues, AuditChainIssue{
				Index:  i,
				ID:     event.ID,
				Reason: "prev_hash mismatch",
			})
		}
		wantHash := auditEventHash(event)
		switch {
		case event.Hash == "":
			result.OK = false
			result.Issues = append(result.Issues, AuditChainIssue{
				Index:  i,
				ID:     event.ID,
				Reason: "hash missing",
			})
		case event.Hash != wantHash:
			result.OK = false
			result.Issues = append(result.Issues, AuditChainIssue{
				Index:  i,
				ID:     event.ID,
				Reason: "hash mismatch",
			})
		}
		wantPrev = wantHash
	}
	result.HeadHash = wantPrev
	return result
}

func auditEventHash(event AuditEvent) string {
	event = normalizeAuditEvent(event)
	event.Hash = ""
	h := sha256.New()
	write := func(value string) {
		_, _ = h.Write([]byte(strconv.Itoa(len(value))))
		_, _ = h.Write([]byte(":"))
		_, _ = h.Write([]byte(value))
	}
	writeField := func(name, value string) {
		write(name)
		write(value)
	}
	writeField("id", event.ID)
	writeField("time", event.Time.UTC().Format(time.RFC3339Nano))
	writeField("prev_hash", event.PrevHash)
	writeField("namespace", event.Namespace)
	writeField("actor", event.Actor)
	writeField("action", event.Action)
	writeField("target_type", event.TargetType)
	writeField("target_id", event.TargetID)
	writeField("worker_id", event.WorkerID)
	writeField("assignment_id", event.AssignmentID)
	writeField("status", event.Status)
	keys := make([]string, 0, len(event.Fields))
	for key := range event.Fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		writeField("field:"+key, event.Fields[key])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func normalizeServiceAccountRecord(record serviceAccountRecord) serviceAccountRecord {
	record.Name = strings.TrimSpace(record.Name)
	record.Namespace = normalizeNamespace(record.Namespace)
	record.Role = normalizeStoredServiceAccountRole(record.Role)
	record.TokenHash = strings.TrimSpace(record.TokenHash)
	if !record.Created.IsZero() {
		record.Created = record.Created.UTC()
	}
	if !record.Updated.IsZero() {
		record.Updated = record.Updated.UTC()
	}
	return record
}

func normalizeOIDCBindingRecord(record oidcBindingRecord) (oidcBindingRecord, error) {
	record.Name = strings.TrimSpace(record.Name)
	if record.Name == "" {
		return oidcBindingRecord{}, fmt.Errorf("oidc binding name required")
	}
	record.Issuer = strings.TrimSpace(record.Issuer)
	if record.Issuer == "" {
		return oidcBindingRecord{}, fmt.Errorf("oidc binding issuer required")
	}
	record.Subject = strings.TrimSpace(record.Subject)
	if record.Subject == "" {
		return oidcBindingRecord{}, fmt.Errorf("oidc binding subject required")
	}
	record.Audience = strings.TrimSpace(record.Audience)
	if record.Audience == "" {
		return oidcBindingRecord{}, fmt.Errorf("oidc binding audience required")
	}
	if strings.TrimSpace(record.Role) == "" {
		return oidcBindingRecord{}, fmt.Errorf("oidc binding role required")
	}
	role, err := normalizeServiceAccountRole(record.Role)
	if err != nil {
		return oidcBindingRecord{}, err
	}
	record.Role = role
	record.Namespace = normalizeNamespace(record.Namespace)
	record.JWKSURL = strings.TrimSpace(record.JWKSURL)
	if record.JWKSURL != "" {
		record.JWKSURL, err = normalizeOIDCURL(record.JWKSURL, "oidc binding jwks_url")
		if err != nil {
			return oidcBindingRecord{}, err
		}
	}
	record.Keys, err = normalizeOIDCKeys(record.Keys)
	if err != nil {
		return oidcBindingRecord{}, err
	}
	if len(record.Keys) == 0 && record.JWKSURL == "" {
		if _, err := oidcDiscoveryURL(record.Issuer); err != nil {
			return oidcBindingRecord{}, fmt.Errorf("oidc binding key or jwks_url required")
		}
	}
	if !record.JWKSFetched.IsZero() {
		record.JWKSFetched = record.JWKSFetched.UTC()
	}
	if !record.Created.IsZero() {
		record.Created = record.Created.UTC()
	}
	if !record.Updated.IsZero() {
		record.Updated = record.Updated.UTC()
	}
	return record, nil
}

func normalizeOIDCKeys(keys []oidcKeyRecord) ([]oidcKeyRecord, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	out := make([]oidcKeyRecord, 0, len(keys))
	for _, key := range keys {
		key.KID = strings.TrimSpace(key.KID)
		key.Alg = strings.TrimSpace(key.Alg)
		if key.Alg == "" {
			key.Alg = "RS256"
		}
		if key.Alg != "RS256" {
			return nil, fmt.Errorf("unsupported oidc key algorithm %q", key.Alg)
		}
		key.PEM = strings.TrimSpace(key.PEM)
		if key.PEM == "" {
			return nil, fmt.Errorf("oidc binding key pem required")
		}
		if _, err := parseRSAPublicKeyPEM(key.PEM); err != nil {
			return nil, fmt.Errorf("oidc binding key %q: %w", key.KID, err)
		}
		out = append(out, key)
	}
	return out, nil
}

func oidcRequestKeys(keys []OIDCKey) []oidcKeyRecord {
	if len(keys) == 0 {
		return nil
	}
	out := make([]oidcKeyRecord, 0, len(keys))
	for _, key := range keys {
		out = append(out, oidcKeyRecord{KID: key.KID, Alg: key.Alg, PEM: key.PEM})
	}
	return out
}

func oidcKeyIDs(keys []oidcKeyRecord) []string {
	if len(keys) == 0 {
		return nil
	}
	ids := make([]string, 0, len(keys))
	for _, key := range keys {
		if key.KID != "" {
			ids = append(ids, key.KID)
		}
	}
	sort.Strings(ids)
	return ids
}

const (
	oidcHTTPTimeout  = 5 * time.Second
	oidcMaxJSONBytes = 1 << 20
)

type oidcDiscoveryDocument struct {
	Issuer  string `json:"issuer"`
	JWKSURI string `json:"jwks_uri"`
}

type oidcJWKSet struct {
	Keys []oidcJWK `json:"keys"`
}

type oidcJWK struct {
	KTY string `json:"kty"`
	KID string `json:"kid,omitempty"`
	Use string `json:"use,omitempty"`
	Alg string `json:"alg,omitempty"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func normalizeOIDCURL(rawURL, field string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", fmt.Errorf("%s required", field)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("%s invalid: %w", field, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("%s must be an absolute http url", field)
	}
	if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return "", fmt.Errorf("%s must use http or https", field)
	}
	parsed.Fragment = ""
	return parsed.String(), nil
}

func oidcDiscoveryURL(issuer string) (string, error) {
	issuer, err := normalizeOIDCURL(issuer, "oidc binding issuer")
	if err != nil {
		return "", err
	}
	parsed, err := url.Parse(issuer)
	if err != nil {
		return "", fmt.Errorf("oidc binding issuer invalid: %w", err)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/.well-known/openid-configuration"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func fetchOIDCBindingKeys(record oidcBindingRecord) ([]oidcKeyRecord, string, error) {
	jwksURL := record.JWKSURL
	if jwksURL == "" {
		discovered, err := fetchOIDCDiscoveryJWKSURL(record.Issuer)
		if err != nil {
			return nil, "", err
		}
		jwksURL = discovered
	}
	keys, err := fetchOIDCJWKSKeys(jwksURL)
	if err != nil {
		return nil, "", err
	}
	return keys, jwksURL, nil
}

func fetchOIDCDiscoveryJWKSURL(issuer string) (string, error) {
	discoveryURL, err := oidcDiscoveryURL(issuer)
	if err != nil {
		return "", err
	}
	var doc oidcDiscoveryDocument
	if err := fetchOIDCJSON(discoveryURL, &doc); err != nil {
		return "", err
	}
	if strings.TrimSpace(doc.Issuer) != strings.TrimSpace(issuer) {
		return "", fmt.Errorf("oidc discovery issuer mismatch")
	}
	jwksURL, err := normalizeOIDCURL(doc.JWKSURI, "oidc discovery jwks_uri")
	if err != nil {
		return "", err
	}
	return jwksURL, nil
}

func fetchOIDCJWKSKeys(jwksURL string) ([]oidcKeyRecord, error) {
	jwksURL, err := normalizeOIDCURL(jwksURL, "oidc binding jwks_url")
	if err != nil {
		return nil, err
	}
	var set oidcJWKSet
	if err := fetchOIDCJSON(jwksURL, &set); err != nil {
		return nil, err
	}
	keys, err := oidcJWKSetKeys(set)
	if err != nil {
		return nil, err
	}
	return normalizeOIDCKeys(keys)
}

func fetchOIDCJSON(rawURL string, out any) error {
	ctx, cancel := context.WithTimeout(context.Background(), oidcHTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("build oidc request: %w", err)
	}
	req.Header.Set("accept", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch oidc json: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("fetch oidc json: status %d", resp.StatusCode)
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, oidcMaxJSONBytes))
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("decode oidc json: %w", err)
	}
	return nil
}

func oidcJWKSetKeys(set oidcJWKSet) ([]oidcKeyRecord, error) {
	out := make([]oidcKeyRecord, 0, len(set.Keys))
	for _, jwk := range set.Keys {
		key, ok, err := oidcJWKKey(jwk)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, key)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("oidc jwks has no rs256 rsa keys")
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].KID != out[j].KID {
			return out[i].KID < out[j].KID
		}
		return out[i].PEM < out[j].PEM
	})
	return out, nil
}

func oidcJWKKey(jwk oidcJWK) (oidcKeyRecord, bool, error) {
	if strings.TrimSpace(jwk.KTY) != "RSA" {
		return oidcKeyRecord{}, false, nil
	}
	if use := strings.TrimSpace(jwk.Use); use != "" && use != "sig" {
		return oidcKeyRecord{}, false, nil
	}
	if alg := strings.TrimSpace(jwk.Alg); alg != "" && alg != "RS256" {
		return oidcKeyRecord{}, false, nil
	}
	n, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(jwk.N))
	if err != nil {
		return oidcKeyRecord{}, false, fmt.Errorf("decode oidc jwk modulus: %w", err)
	}
	if len(n) == 0 {
		return oidcKeyRecord{}, false, fmt.Errorf("invalid oidc jwk modulus")
	}
	e, err := oidcJWKExponent(strings.TrimSpace(jwk.E))
	if err != nil {
		return oidcKeyRecord{}, false, err
	}
	pub := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: e}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return oidcKeyRecord{}, false, fmt.Errorf("marshal oidc jwk public key: %w", err)
	}
	pemData := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	return oidcKeyRecord{KID: strings.TrimSpace(jwk.KID), Alg: "RS256", PEM: string(pemData)}, true, nil
}

func oidcJWKExponent(data string) (int, error) {
	raw, err := base64.RawURLEncoding.DecodeString(data)
	if err != nil {
		return 0, fmt.Errorf("decode oidc jwk exponent: %w", err)
	}
	if len(raw) == 0 || len(raw) > 4 {
		return 0, fmt.Errorf("invalid oidc jwk exponent")
	}
	var out int
	for _, b := range raw {
		out = (out << 8) + int(b)
	}
	if out < 3 {
		return 0, fmt.Errorf("invalid oidc jwk exponent")
	}
	return out, nil
}

func (s *Store) refreshOIDCBindingKeys(name string) (oidcBindingRecord, bool) {
	s.mu.Lock()
	record, ok := s.oidcBindings[name]
	if ok {
		record = cloneOIDCBindingRecord(record)
	}
	s.mu.Unlock()
	if !ok {
		return oidcBindingRecord{}, false
	}
	keys, jwksURL, err := fetchOIDCBindingKeys(record)
	if err != nil {
		return oidcBindingRecord{}, false
	}

	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.oidcBindings[name]
	if !ok {
		return oidcBindingRecord{}, false
	}
	if !sameOIDCRefreshBinding(current, record, jwksURL) {
		return cloneOIDCBindingRecord(current), true
	}
	current.JWKSURL = jwksURL
	current.JWKSFetched = now
	current.Keys = keys
	current.Updated = now
	s.oidcBindings[name] = current
	if err := s.persistLocked(); err != nil {
		return oidcBindingRecord{}, false
	}
	return cloneOIDCBindingRecord(current), true
}

func sameOIDCRefreshBinding(current, base oidcBindingRecord, jwksURL string) bool {
	if current.Issuer != base.Issuer || current.Subject != base.Subject || current.Audience != base.Audience {
		return false
	}
	if current.Namespace != base.Namespace || current.Role != base.Role {
		return false
	}
	if current.JWKSURL == base.JWKSURL {
		return true
	}
	return base.JWKSURL == "" && current.JWKSURL == jwksURL
}

func oidcBindingUsesDynamicKeys(record oidcBindingRecord) bool {
	return record.JWKSURL != "" || len(record.Keys) == 0
}

func (s *Store) authenticateOIDCBearer(token string) (authenticatedPrincipal, bool) {
	parsed, err := parseOIDCJWT(token)
	if err != nil {
		return authenticatedPrincipal{}, false
	}
	s.mu.Lock()
	now := s.now().UTC()
	bindings := s.sortedOIDCBindingsLocked()
	s.mu.Unlock()
	for _, binding := range bindings {
		if !oidcClaimsMatch(binding, parsed.claims, now) {
			continue
		}
		if verifyOIDCSignature(binding.Keys, parsed) {
			return authenticatedPrincipal{
				Actor:     "oidc:" + binding.Name,
				Namespace: binding.Namespace,
				Role:      binding.Role,
			}, true
		}
		if parsed.header.Alg != "RS256" || !oidcBindingUsesDynamicKeys(binding) {
			continue
		}
		if refreshed, ok := s.refreshOIDCBindingKeys(binding.Name); ok && oidcClaimsMatch(refreshed, parsed.claims, now) && verifyOIDCSignature(refreshed.Keys, parsed) {
			return authenticatedPrincipal{
				Actor:     "oidc:" + refreshed.Name,
				Namespace: refreshed.Namespace,
				Role:      refreshed.Role,
			}, true
		}
	}
	return authenticatedPrincipal{}, false
}

type parsedOIDCJWT struct {
	header       oidcJWTHeader
	claims       oidcJWTClaims
	signingInput string
	signature    []byte
}

type oidcJWTHeader struct {
	Alg string `json:"alg"`
	KID string `json:"kid,omitempty"`
}

type oidcJWTClaims struct {
	Issuer    string       `json:"iss"`
	Subject   string       `json:"sub"`
	Audience  oidcAudience `json:"aud"`
	Expires   int64        `json:"exp"`
	NotBefore int64        `json:"nbf,omitempty"`
}

type oidcAudience []string

func (a *oidcAudience) UnmarshalJSON(data []byte) error {
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		*a = []string{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return err
	}
	*a = many
	return nil
}

func (a oidcAudience) contains(value string) bool {
	for _, item := range a {
		if item == value {
			return true
		}
	}
	return false
}

func parseOIDCJWT(token string) (parsedOIDCJWT, error) {
	token = strings.TrimSpace(token)
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return parsedOIDCJWT{}, fmt.Errorf("jwt must have three parts")
	}
	headerData, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return parsedOIDCJWT{}, fmt.Errorf("decode jwt header: %w", err)
	}
	payloadData, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return parsedOIDCJWT{}, fmt.Errorf("decode jwt payload: %w", err)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return parsedOIDCJWT{}, fmt.Errorf("decode jwt signature: %w", err)
	}
	var header oidcJWTHeader
	if err := json.Unmarshal(headerData, &header); err != nil {
		return parsedOIDCJWT{}, fmt.Errorf("parse jwt header: %w", err)
	}
	var claims oidcJWTClaims
	if err := json.Unmarshal(payloadData, &claims); err != nil {
		return parsedOIDCJWT{}, fmt.Errorf("parse jwt claims: %w", err)
	}
	return parsedOIDCJWT{
		header:       header,
		claims:       claims,
		signingInput: parts[0] + "." + parts[1],
		signature:    signature,
	}, nil
}

func oidcClaimsMatch(binding oidcBindingRecord, claims oidcJWTClaims, now time.Time) bool {
	if binding.Issuer != strings.TrimSpace(claims.Issuer) {
		return false
	}
	if binding.Subject != strings.TrimSpace(claims.Subject) {
		return false
	}
	if !claims.Audience.contains(binding.Audience) {
		return false
	}
	if claims.Expires == 0 {
		return false
	}
	const skew = time.Minute
	if now.After(time.Unix(claims.Expires, 0).Add(skew)) {
		return false
	}
	if claims.NotBefore != 0 && now.Add(skew).Before(time.Unix(claims.NotBefore, 0)) {
		return false
	}
	return true
}

func verifyOIDCSignature(keys []oidcKeyRecord, parsed parsedOIDCJWT) bool {
	if parsed.header.Alg != "RS256" {
		return false
	}
	sum := sha256.Sum256([]byte(parsed.signingInput))
	for _, key := range keys {
		if key.Alg != "" && key.Alg != parsed.header.Alg {
			continue
		}
		if parsed.header.KID != "" && key.KID != "" && key.KID != parsed.header.KID {
			continue
		}
		pub, err := parseRSAPublicKeyPEM(key.PEM)
		if err != nil {
			continue
		}
		if rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], parsed.signature) == nil {
			return true
		}
	}
	return false
}

func parseRSAPublicKeyPEM(data string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(data))
	if block == nil {
		return nil, fmt.Errorf("parse public key pem")
	}
	if key, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		rsaKey, ok := key.(*rsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("public key is %T, want rsa", key)
		}
		return rsaKey, nil
	}
	key, err := x509.ParsePKCS1PublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse rsa public key: %w", err)
	}
	return key, nil
}

func normalizeServiceAccountRole(role string) (string, error) {
	role = strings.TrimSpace(role)
	if role == "" {
		return ServiceAccountRoleAdmin, nil
	}
	switch role {
	case ServiceAccountRoleViewer, ServiceAccountRoleOperator, ServiceAccountRoleAdmin:
		return role, nil
	default:
		return "", fmt.Errorf("unknown service account role %q", role)
	}
}

func normalizeStoredServiceAccountRole(role string) string {
	role = strings.TrimSpace(role)
	if role == "" {
		return ServiceAccountRoleAdmin
	}
	switch role {
	case ServiceAccountRoleViewer, ServiceAccountRoleOperator, ServiceAccountRoleAdmin:
		return role
	default:
		return ""
	}
}

func normalizeNamespace(namespace string) string {
	return strings.TrimSpace(namespace)
}

func namespaceMatches(resource, filter string) bool {
	filter = normalizeNamespace(filter)
	if filter == "" {
		return true
	}
	return normalizeNamespace(resource) == filter
}

func tokenHash(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func normalizeActor(actor string) string {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return "controller"
	}
	return actor
}

func auditReportStatus(status string) bool {
	switch status {
	case "running", "ready", "leased", "claimed", "draining", "restarting":
		return false
	default:
		return strings.TrimSpace(status) != ""
	}
}

func imagePrepareArgs(sourceRef, imageRef string, force bool) []string {
	args := []string{"image", "pull", "-tag", imageRef}
	if force {
		args = append(args, "-force")
	}
	return append(args, sourceRef)
}

func imageGCArgs(olderThan string, apply bool) []string {
	args := []string{"image", "gc"}
	if apply {
		args = append(args, "-yes")
	} else {
		args = append(args, "-dry-run")
	}
	if olderThan != "" {
		args = append(args, "-older-than", olderThan)
	}
	return args
}

func lifecyclePolicyArgs(req LifecyclePolicyRequest) (string, []string, error) {
	vmName := strings.TrimSpace(req.VMName)
	if vmName == "" {
		return "", nil, fmt.Errorf("lifecycle policy vm_name required")
	}
	idleTimeout, err := normalizeDurationString(req.IdleTimeout, "lifecycle policy idle_timeout")
	if err != nil {
		return "", nil, err
	}
	maxAge, err := normalizeDurationString(req.MaxAge, "lifecycle policy max_age")
	if err != nil {
		return "", nil, err
	}
	if req.RunBudget < 0 {
		return "", nil, fmt.Errorf("lifecycle policy run_budget must be non-negative")
	}
	fields := lifecyclePolicyFields(idleTimeout, maxAge, req.RunBudget)
	if req.Clear {
		if len(fields) > 0 {
			return "", nil, fmt.Errorf("lifecycle policy clear cannot include thresholds")
		}
		return vmName, []string{"policy", vmName, "clear"}, nil
	}
	if len(fields) == 0 {
		return "", nil, fmt.Errorf("lifecycle policy threshold required")
	}
	return vmName, append([]string{"policy", vmName, "set"}, fields...), nil
}

func storageBudgetArgs(req StorageBudgetRequest) ([]string, error) {
	target := strings.TrimSpace(req.Target)
	if req.Clear {
		if target != "" || req.WarnPct != nil || req.HardPct != nil {
			return nil, fmt.Errorf("storage budget clear cannot include thresholds")
		}
		return []string{"storage", "budget", "clear"}, nil
	}
	if target == "" {
		return nil, fmt.Errorf("storage budget target required")
	}
	warn := 80
	if req.WarnPct != nil {
		warn = *req.WarnPct
	}
	hard := 95
	if req.HardPct != nil {
		hard = *req.HardPct
	}
	if warn < 0 || warn > 100 {
		return nil, fmt.Errorf("storage budget warn_pct must be in [0,100]")
	}
	if hard < 0 || hard > 100 {
		return nil, fmt.Errorf("storage budget hard_pct must be in [0,100]")
	}
	if warn > 0 && hard > 0 && warn > hard {
		return nil, fmt.Errorf("storage budget warn_pct (%d) must not exceed hard_pct (%d)", warn, hard)
	}
	args := []string{"storage", "budget", "set", "-target", target}
	if req.WarnPct != nil {
		args = append(args, "-warn", strconv.Itoa(warn))
	}
	if req.HardPct != nil {
		args = append(args, "-hard", strconv.Itoa(hard))
	}
	return args, nil
}

func storagePruneArgs(req StoragePruneRequest) ([]string, error) {
	category := strings.TrimSpace(req.Category)
	if category != "" && category != "build-scratch" {
		return nil, fmt.Errorf("storage prune category %q unsupported", category)
	}
	olderThan, err := normalizeDurationString(req.OlderThan, "storage prune older_than")
	if err != nil {
		return nil, err
	}
	args := []string{"storage", "prune"}
	if category != "" {
		args = append(args, category)
	}
	if req.Apply {
		args = append(args, "-apply")
	}
	if olderThan != "" {
		args = append(args, "-older-than", olderThan)
	}
	return args, nil
}

func lifecyclePolicyFields(idleTimeout, maxAge string, runBudget int) []string {
	var fields []string
	if idleTimeout != "" {
		fields = append(fields, "idle="+idleTimeout)
	}
	if maxAge != "" {
		fields = append(fields, "max-age="+maxAge)
	}
	if runBudget > 0 {
		fields = append(fields, "run-budget="+strconv.Itoa(runBudget))
	}
	return fields
}

func normalizeDurationString(value, name string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return "", fmt.Errorf("%s invalid: %w", name, err)
	}
	if duration <= 0 {
		return "", fmt.Errorf("%s must be positive", name)
	}
	return value, nil
}

func labelsMatch(have, want map[string]string) bool {
	for key, value := range want {
		if have[key] != value {
			return false
		}
	}
	return true
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func sortedUniqueStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, value := range in {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func cloneLabels(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func normalizeEnv(in map[string]string) (map[string]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("env key required")
		}
		if strings.Contains(key, "=") {
			return nil, fmt.Errorf("env key %q must not contain =", key)
		}
		if _, ok := out[key]; ok {
			return nil, fmt.Errorf("env key %q repeated", key)
		}
		out[key] = value
	}
	return out, nil
}
