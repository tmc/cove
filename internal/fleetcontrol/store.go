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
	"encoding/xml"
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
	mu                sync.Mutex
	path              string
	ttl               time.Duration
	assignmentTTL     time.Duration
	now               func() time.Time
	hosts             map[string]HostRecord
	assignments       map[string]Assignment
	warmPools         map[string]WarmPool
	plans             []PlacementPlan
	preparations      []ImagePrepareResult
	imageGCRuns       []ImageGCResult
	lifecycleRuns     []LifecyclePolicyResult
	storageBudgetRuns []StorageBudgetResult
	storagePruneRuns  []StoragePruneResult
	audit             []AuditEvent
	metering          []SandboxMeteringRecord
	reports           []AssignmentReport
	accounts          map[string]serviceAccountRecord
	oidcBindings      map[string]oidcBindingRecord
	samlBindings      map[string]samlBindingRecord
	samlReplays       map[string]samlReplayRecord
	samlSessions      map[string]samlSessionRecord
}

type storeFile struct {
	Hosts             []HostRecord            `json:"hosts"`
	Assignments       []Assignment            `json:"assignments,omitempty"`
	WarmPools         []WarmPool              `json:"warm_pools,omitempty"`
	PlacementPlans    []PlacementPlan         `json:"placement_plans,omitempty"`
	ImagePreparations []ImagePrepareResult    `json:"image_preparations,omitempty"`
	ImageGCRuns       []ImageGCResult         `json:"image_gc_runs,omitempty"`
	LifecycleRuns     []LifecyclePolicyResult `json:"lifecycle_runs,omitempty"`
	StorageBudgetRuns []StorageBudgetResult   `json:"storage_budget_runs,omitempty"`
	StoragePruneRuns  []StoragePruneResult    `json:"storage_prune_runs,omitempty"`
	AuditEvents       []AuditEvent            `json:"audit_events,omitempty"`
	MeteringRecords   []SandboxMeteringRecord `json:"metering_records,omitempty"`
	AssignmentReports []AssignmentReport      `json:"assignment_reports,omitempty"`
	ServiceAccounts   []serviceAccountRecord  `json:"service_accounts,omitempty"`
	OIDCBindings      []oidcBindingRecord     `json:"oidc_bindings,omitempty"`
	SAMLBindings      []samlBindingRecord     `json:"saml_bindings,omitempty"`
	SAMLReplays       []samlReplayRecord      `json:"saml_replays,omitempty"`
	SAMLSessions      []samlSessionRecord     `json:"saml_sessions,omitempty"`
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

type samlBindingRecord struct {
	Name            string    `json:"name"`
	EntityID        string    `json:"entity_id"`
	Subject         string    `json:"subject,omitempty"`
	SSOURL          string    `json:"sso_url"`
	Audience        string    `json:"audience"`
	Namespace       string    `json:"namespace,omitempty"`
	Role            string    `json:"role,omitempty"`
	CertificatePEM  string    `json:"certificate_pem"`
	MetadataURL     string    `json:"metadata_url,omitempty"`
	MetadataFetched time.Time `json:"metadata_fetched,omitempty"`
	Created         time.Time `json:"created,omitempty"`
	Updated         time.Time `json:"updated,omitempty"`
}

type samlReplayRecord struct {
	Binding     string    `json:"binding"`
	AssertionID string    `json:"assertion_id"`
	Expires     time.Time `json:"expires"`
}

type samlSessionRecord struct {
	TokenHash string    `json:"token_sha256"`
	Binding   string    `json:"binding"`
	Subject   string    `json:"subject,omitempty"`
	Namespace string    `json:"namespace,omitempty"`
	Role      string    `json:"role,omitempty"`
	Expires   time.Time `json:"expires"`
	Created   time.Time `json:"created,omitempty"`
	Updated   time.Time `json:"updated,omitempty"`
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
		path:              strings.TrimSpace(path),
		ttl:               ttl,
		assignmentTTL:     DefaultAssignmentTTL,
		now:               time.Now,
		hosts:             make(map[string]HostRecord),
		assignments:       make(map[string]Assignment),
		warmPools:         make(map[string]WarmPool),
		plans:             nil,
		preparations:      nil,
		imageGCRuns:       nil,
		lifecycleRuns:     nil,
		storageBudgetRuns: nil,
		storagePruneRuns:  nil,
		audit:             nil,
		metering:          nil,
		reports:           nil,
		accounts:          make(map[string]serviceAccountRecord),
		oidcBindings:      make(map[string]oidcBindingRecord),
		samlBindings:      make(map[string]samlBindingRecord),
		samlReplays:       make(map[string]samlReplayRecord),
		samlSessions:      make(map[string]samlSessionRecord),
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
		host.Capabilities = sortedUniqueStrings(host.Capabilities)
		host.ImageRefs, host.ImageDetails = normalizeWorkerImageInventory(host.ImageRefs, host.ImageDetails)
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
		assignment.ImageManifestDigest = strings.TrimSpace(assignment.ImageManifestDigest)
		assignment.ImageDigestRef = strings.TrimSpace(assignment.ImageDigestRef)
		assignment.ImagePlatform = strings.TrimSpace(assignment.ImagePlatform)
		assignment.AntiAffinityKey = strings.TrimSpace(assignment.AntiAffinityKey)
		assignment.RequiredLabels = cloneLabels(assignment.RequiredLabels)
		assignment.RequiredCapabilities = sortedUniqueStrings(assignment.RequiredCapabilities)
		assignment.QueueTTL = ""
		if !assignment.QueueExpires.IsZero() {
			assignment.QueueExpires = assignment.QueueExpires.UTC()
		}
		assignment.RunTimeout = strings.TrimSpace(assignment.RunTimeout)
		if assignment.MaxAttempts < 0 {
			assignment.MaxAttempts = 0
		}
		if assignment.Attempt < 0 {
			assignment.Attempt = 0
		}
		assignment.RetryDelay = strings.TrimSpace(assignment.RetryDelay)
		if !assignment.RetryAt.IsZero() {
			assignment.RetryAt = assignment.RetryAt.UTC()
		}
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
	for _, plan := range file.PlacementPlans {
		plan = normalizePlacementPlan(plan)
		if plan.ID == "" || plan.Created.IsZero() || plan.Policy == "" {
			continue
		}
		s.plans = append(s.plans, plan)
	}
	for _, prep := range file.ImagePreparations {
		prep = normalizeImagePrepareResult(prep)
		if prep.ID == "" || prep.Created.IsZero() || prep.SourceRef == "" || prep.ImageRef == "" {
			continue
		}
		s.preparations = append(s.preparations, prep)
	}
	for _, run := range file.ImageGCRuns {
		run = normalizeImageGCResult(run)
		if run.ID == "" || run.Created.IsZero() {
			continue
		}
		s.imageGCRuns = append(s.imageGCRuns, run)
	}
	for _, run := range file.LifecycleRuns {
		run = normalizeLifecyclePolicyResult(run)
		if run.ID == "" || run.Created.IsZero() || run.VMName == "" {
			continue
		}
		s.lifecycleRuns = append(s.lifecycleRuns, run)
	}
	for _, run := range file.StorageBudgetRuns {
		run = normalizeStorageBudgetResult(run)
		if run.ID == "" || run.Created.IsZero() {
			continue
		}
		s.storageBudgetRuns = append(s.storageBudgetRuns, run)
	}
	for _, run := range file.StoragePruneRuns {
		run = normalizeStoragePruneResult(run)
		if run.ID == "" || run.Created.IsZero() {
			continue
		}
		s.storagePruneRuns = append(s.storagePruneRuns, run)
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
	for _, report := range file.AssignmentReports {
		report = normalizeAssignmentReport(report)
		if report.AssignmentID == "" || report.Report.ID == "" || report.Report.Status == "" || report.Report.Time.IsZero() {
			continue
		}
		s.reports = append(s.reports, report)
	}
	if len(file.AssignmentReports) == 0 {
		for _, assignment := range s.assignments {
			if assignment.LastReport != nil {
				s.reports = append(s.reports, assignmentReportFromAssignment(assignment))
			}
		}
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
	for _, binding := range file.SAMLBindings {
		binding, err := normalizeSAMLBindingRecord(binding)
		if err != nil {
			continue
		}
		s.samlBindings[binding.Name] = binding
	}
	for _, replay := range file.SAMLReplays {
		replay = normalizeSAMLReplayRecord(replay)
		if replay.Binding == "" || replay.AssertionID == "" || replay.Expires.IsZero() {
			continue
		}
		s.samlReplays[samlReplayKey(replay.Binding, replay.AssertionID)] = replay
	}
	for _, session := range file.SAMLSessions {
		session = normalizeSAMLSessionRecord(session)
		if session.TokenHash == "" || session.Binding == "" || session.Role == "" || session.Expires.IsZero() {
			continue
		}
		s.samlSessions[session.TokenHash] = session
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

func (s *Store) ReconcilePlan() ReconcileResult {
	now := s.now().UTC()
	s.mu.Lock()
	shadow := &Store{
		ttl:               s.ttl,
		assignmentTTL:     s.assignmentTTL,
		now:               func() time.Time { return now },
		hosts:             cloneHostMap(s.hosts),
		assignments:       cloneAssignmentMap(s.assignments),
		warmPools:         cloneWarmPoolMap(s.warmPools),
		plans:             clonePlacementPlans(s.plans),
		preparations:      cloneImagePrepareResults(s.preparations),
		imageGCRuns:       cloneImageGCResults(s.imageGCRuns),
		lifecycleRuns:     cloneLifecyclePolicyResults(s.lifecycleRuns),
		storageBudgetRuns: cloneStorageBudgetResults(s.storageBudgetRuns),
		storagePruneRuns:  cloneStoragePruneResults(s.storagePruneRuns),
		metering:          cloneSandboxMeteringRecords(s.metering),
		reports:           cloneAssignmentReports(s.reports),
	}
	s.mu.Unlock()
	return shadow.reconcileLocked(now)
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
			"expired_assignments":  strconv.Itoa(len(result.ExpiredAssignments)),
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
	imageRefs, imageDetails := normalizeWorkerImageInventory(h.ImageRefs, h.ImageDetails)
	record := HostRecord{
		ID:           id,
		Host:         strings.TrimSpace(h.Host),
		Address:      strings.TrimSpace(h.Address),
		Version:      strings.TrimSpace(h.Version),
		Labels:       cloneLabels(h.Labels),
		Capabilities: sortedUniqueStrings(h.Capabilities),
		ImageRefs:    imageRefs,
		ImageDetails: imageDetails,
		Capacity:     h.Capacity,
		Status:       "ready",
		LastSeen:     now,
		Expires:      now.Add(s.ttl),
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
		record.Quarantined = old.Quarantined
		record.QuarantineReason = old.QuarantineReason
		record.QuarantinedAt = old.QuarantinedAt
		record.Status = workerStatus(now, record)
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
	record.Status = workerStatus(now, record)
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

func (s *Store) DrainWorker(id, reason string) (WorkerDrainResult, error) {
	return s.DrainWorkerActor("controller", id, reason)
}

func (s *Store) DrainWorkerActor(actor, id, reason string) (WorkerDrainResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return WorkerDrainResult{}, fmt.Errorf("worker id required")
	}
	now := s.now().UTC()
	actor = normalizeActor(actor)
	reason = strings.TrimSpace(reason)

	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.hosts[id]
	if !ok {
		return WorkerDrainResult{}, fmt.Errorf("worker %q not registered", id)
	}
	record.Cordoned = true
	record.CordonReason = reason
	record.CordonedAt = now
	record.Status = workerStatus(now, record)
	s.hosts[id] = record

	result := WorkerDrainResult{Worker: s.statusLocked(record)}
	for _, assignment := range s.sortedAssignmentsLocked() {
		if assignment.WorkerID != id || assignment.SandboxID == "" || assignment.SandboxRole != sandboxRoleRun {
			continue
		}
		if sandboxTerminalStatus(assignment.Status) {
			result.Skipped = append(result.Skipped, WorkerDrainSkip{
				SandboxID: assignment.SandboxID,
				Status:    assignment.Status,
				Reason:    "terminal",
			})
			continue
		}
		stopped, err := s.stopSandboxLocked(now, actor, assignment.SandboxID, "sandbox.drain", "")
		if err != nil {
			result.Skipped = append(result.Skipped, WorkerDrainSkip{
				SandboxID: assignment.SandboxID,
				Status:    assignment.Status,
				Reason:    err.Error(),
			})
			continue
		}
		result.Sandboxes = append(result.Sandboxes, stopped)
	}
	s.appendAuditLocked(now, AuditEvent{
		Actor:      actor,
		Action:     "worker.drain",
		TargetType: "worker",
		TargetID:   id,
		WorkerID:   id,
		Fields: map[string]string{
			"reason":    reason,
			"sandboxes": strconv.Itoa(len(result.Sandboxes)),
			"skipped":   strconv.Itoa(len(result.Skipped)),
		},
	})
	if err := s.persistLocked(); err != nil {
		return WorkerDrainResult{}, err
	}
	return result, nil
}

func (s *Store) EvacuateWorker(id string, req WorkerEvacuationRequest) (WorkerEvacuationResult, error) {
	return s.EvacuateWorkerActor("controller", id, req)
}

func (s *Store) EvacuateWorkerActor(actor, id string, req WorkerEvacuationRequest) (WorkerEvacuationResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return WorkerEvacuationResult{}, fmt.Errorf("worker id required")
	}
	now := s.now().UTC()
	actor = normalizeActor(actor)
	reason := strings.TrimSpace(req.Reason)

	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.hosts[id]
	if !ok {
		return WorkerEvacuationResult{}, fmt.Errorf("worker %q not registered", id)
	}
	if !req.Apply {
		assignments := cloneAssignmentMap(s.assignments)
		defer func() {
			s.assignments = assignments
		}()
	}
	result := WorkerEvacuationResult{
		Worker: s.statusLocked(record),
		Reason: reason,
		Apply:  req.Apply,
		Force:  req.Force,
	}
	if req.Apply {
		record.Cordoned = true
		record.CordonReason = reason
		record.CordonedAt = now
		record.Status = workerStatus(now, record)
		s.hosts[id] = record
		result.Worker = s.statusLocked(record)
	}

	for _, assignment := range s.sortedAssignmentsLocked() {
		if assignment.WorkerID != id && assignment.LeasedTo != id {
			continue
		}
		item := evacuationAssignment(assignment)
		status := normalizeOperationStatus(assignment.Status)
		if !openAssignmentStatus(status) && !sandboxPendingStopStatus(status) && status != "restarting" {
			item.Action = "ignore"
			item.Reason = "terminal"
			continue
		}
		if status == "pending" && assignment.LeasedTo == "" {
			candidates := s.evacuationCandidatesLocked(id, assignment)
			if len(candidates) > 0 {
				item.Action = "requeue"
				item.TargetWorkerID = candidates[0].WorkerID
				item.Candidates = candidates
				if req.Apply {
					assignment.WorkerID = candidates[0].WorkerID
					assignment.Updated = now
					s.assignments[assignment.ID] = assignment
					result.Requeued = append(result.Requeued, cloneAssignment(assignment))
					s.appendAuditLocked(now, AuditEvent{
						Actor:        actor,
						Namespace:    assignment.Namespace,
						Action:       "assignment.evacuate",
						TargetType:   "assignment",
						TargetID:     assignment.ID,
						WorkerID:     id,
						AssignmentID: assignment.ID,
						Status:       assignment.Status,
						Fields: map[string]string{
							"reason":     reason,
							"new_worker": assignment.WorkerID,
						},
					})
				} else {
					assignment.WorkerID = candidates[0].WorkerID
					s.assignments[assignment.ID] = assignment
				}
				result.Assignments = append(result.Assignments, item)
				continue
			}
			if req.Force {
				item.Action = "cancel"
				item.Reason = evacuationPendingBlockReason(assignment)
				if req.Apply {
					assignment.Status = "canceled"
					assignment.Updated = now
					s.assignments[assignment.ID] = assignment
					result.Canceled = append(result.Canceled, assignment.ID)
					s.appendAuditLocked(now, AuditEvent{
						Actor:        actor,
						Namespace:    assignment.Namespace,
						Action:       "assignment.cancel",
						TargetType:   "assignment",
						TargetID:     assignment.ID,
						WorkerID:     id,
						AssignmentID: assignment.ID,
						Status:       assignment.Status,
						Fields: map[string]string{
							"reason":    reason,
							"operation": "worker.evacuate",
						},
					})
				}
				result.Assignments = append(result.Assignments, item)
				continue
			}
			item.Action = "blocked"
			item.Reason = evacuationPendingBlockReason(assignment)
			result.Assignments = append(result.Assignments, item)
			result.Blocked = append(result.Blocked, item)
			continue
		}
		if assignment.SandboxID != "" && assignment.SandboxRole == sandboxRoleRun {
			if err := requireSandboxLeaseHolder(now, assignment.SandboxID, &assignment, ""); err != nil {
				item.Action = "blocked"
				item.Reason = err.Error()
				result.Assignments = append(result.Assignments, item)
				result.Blocked = append(result.Blocked, item)
				continue
			}
			item.Action = "drain"
			item.Reason = "hosted sandbox"
			if req.Apply {
				stopped, err := s.stopSandboxLocked(now, actor, assignment.SandboxID, "sandbox.evacuate", "")
				if err != nil {
					item.Action = "blocked"
					item.Reason = err.Error()
					result.Blocked = append(result.Blocked, item)
				} else {
					result.Sandboxes = append(result.Sandboxes, stopped)
					if stopped.Canceled {
						result.Canceled = append(result.Canceled, assignment.ID)
					}
				}
			}
			result.Assignments = append(result.Assignments, item)
			continue
		}
		item.Action = "blocked"
		item.Reason = evacuationActiveBlockReason(assignment, id)
		result.Assignments = append(result.Assignments, item)
		result.Blocked = append(result.Blocked, item)
	}
	if req.Apply {
		result.Applied = true
		s.appendAuditLocked(now, AuditEvent{
			Actor:      actor,
			Action:     "worker.evacuate",
			TargetType: "worker",
			TargetID:   id,
			WorkerID:   id,
			Fields: map[string]string{
				"reason":    reason,
				"requeued":  strconv.Itoa(len(result.Requeued)),
				"sandboxes": strconv.Itoa(len(result.Sandboxes)),
				"canceled":  strconv.Itoa(len(result.Canceled)),
				"blocked":   strconv.Itoa(len(result.Blocked)),
				"force":     strconv.FormatBool(req.Force),
			},
		})
		if err := s.persistLocked(); err != nil {
			return WorkerEvacuationResult{}, err
		}
	}
	return result, nil
}

func (s *Store) QuarantineWorker(id, reason string) (HostRecord, error) {
	return s.QuarantineWorkerActor("controller", id, reason)
}

func (s *Store) QuarantineWorkerActor(actor, id, reason string) (HostRecord, error) {
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
	record.Quarantined = true
	record.QuarantineReason = strings.TrimSpace(reason)
	record.QuarantinedAt = now
	record.Status = workerStatus(now, record)
	s.hosts[id] = record
	s.appendAuditLocked(now, AuditEvent{
		Actor:      actor,
		Action:     "worker.quarantine",
		TargetType: "worker",
		TargetID:   id,
		WorkerID:   id,
		Fields:     map[string]string{"reason": record.QuarantineReason},
	})
	if err := s.persistLocked(); err != nil {
		return HostRecord{}, err
	}
	return s.statusLocked(record), nil
}

func (s *Store) UnquarantineWorker(id string) (HostRecord, error) {
	return s.UnquarantineWorkerActor("controller", id)
}

func (s *Store) UnquarantineWorkerActor(actor, id string) (HostRecord, error) {
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
	record.Quarantined = false
	record.QuarantineReason = ""
	record.QuarantinedAt = time.Time{}
	record.Status = workerStatus(now, record)
	s.hosts[id] = record
	s.appendAuditLocked(now, AuditEvent{
		Actor:      actor,
		Action:     "worker.unquarantine",
		TargetType: "worker",
		TargetID:   id,
		WorkerID:   id,
	})
	if err := s.persistLocked(); err != nil {
		return HostRecord{}, err
	}
	return s.statusLocked(record), nil
}

func (s *Store) DecommissionWorker(id, reason string) (WorkerDecommissionResult, error) {
	return s.DecommissionWorkerActor("controller", id, reason, false)
}

func (s *Store) DecommissionWorkerActor(actor, id, reason string, force bool) (WorkerDecommissionResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return WorkerDecommissionResult{}, fmt.Errorf("worker id required")
	}
	now := s.now().UTC()
	actor = normalizeActor(actor)
	reason = strings.TrimSpace(reason)

	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.hosts[id]
	if !ok {
		return WorkerDecommissionResult{}, fmt.Errorf("worker %q not registered", id)
	}
	result := WorkerDecommissionResult{
		Worker: s.statusLocked(record),
		Reason: reason,
		Force:  force,
	}
	var cancelable []Assignment
	for _, assignment := range s.sortedAssignmentsLocked() {
		if assignment.WorkerID != id && assignment.LeasedTo != id {
			continue
		}
		if !decommissionBlocksWorker(assignment.Status) {
			continue
		}
		if force && decommissionCancelsAssignment(assignment, id) {
			cancelable = append(cancelable, assignment)
			continue
		}
		result.Blocked = append(result.Blocked, WorkerDecommissionBlock{
			AssignmentID: assignment.ID,
			Status:       assignment.Status,
			Reason:       decommissionBlockReason(assignment, id, force),
		})
	}
	if len(result.Blocked) > 0 {
		return result, fmt.Errorf("worker %q has active assignments: %s", id, strings.Join(decommissionBlockIDs(result.Blocked), ", "))
	}
	for _, assignment := range cancelable {
		assignment.Status = "canceled"
		assignment.Updated = now
		s.assignments[assignment.ID] = assignment
		result.Canceled = append(result.Canceled, assignment.ID)
		s.appendAuditLocked(now, AuditEvent{
			Actor:        actor,
			Namespace:    assignment.Namespace,
			Action:       "assignment.cancel",
			TargetType:   "assignment",
			TargetID:     assignment.ID,
			WorkerID:     id,
			AssignmentID: assignment.ID,
			Status:       assignment.Status,
			Fields: map[string]string{
				"reason":    reason,
				"operation": "worker.decommission",
			},
		})
	}
	fields := map[string]string{
		"reason":   reason,
		"force":    strconv.FormatBool(force),
		"canceled": strconv.Itoa(len(result.Canceled)),
		"blocked":  strconv.Itoa(len(result.Blocked)),
	}
	if len(result.Canceled) > 0 {
		fields["canceled_assignments"] = strings.Join(result.Canceled, ",")
	}
	delete(s.hosts, id)
	result.Removed = true
	s.appendAuditLocked(now, AuditEvent{
		Actor:      actor,
		Action:     "worker.decommission",
		TargetType: "worker",
		TargetID:   id,
		WorkerID:   id,
		Fields:     fields,
	})
	if err := s.persistLocked(); err != nil {
		return WorkerDecommissionResult{}, err
	}
	return result, nil
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
	record.Status = workerStatus(now, record)
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
				if !canceledAssignmentReportAllowed(assignment, status, id) {
					return HostRecord{}, fmt.Errorf("assignment %q is not leased to %q", r.AssignmentID, id)
				}
			}
			if assignment.LeasedTo != "" && assignment.LeasedTo != id {
				return HostRecord{}, fmt.Errorf("assignment %q leased to %q", r.AssignmentID, assignment.LeasedTo)
			}
		}
	}
	record.Report = &r
	record.LastSeen = received
	record.Expires = received.Add(s.ttl)
	record.Status = workerStatus(received, record)
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
			s.reports = append(s.reports, assignmentReportFromAssignment(assignment))
			if assignment.SandboxID != "" && assignment.SandboxRole == sandboxRoleStop {
				s.finishSandboxStopLocked(received, assignment.SandboxID, storedStatus)
			}
			if auditReportStatus(storedStatus) {
				fields := map[string]string{"exit_code": strconv.Itoa(r.ExitCode)}
				if assignment.SandboxID != "" {
					fields["sandbox_id"] = assignment.SandboxID
					fields["sandbox_role"] = assignment.SandboxRole
				}
				s.appendAuditLocked(received, AuditEvent{
					Actor:        "worker:" + id,
					Namespace:    assignment.Namespace,
					Action:       "assignment.report",
					TargetType:   "assignment",
					TargetID:     assignment.ID,
					WorkerID:     id,
					AssignmentID: assignment.ID,
					Status:       storedStatus,
					Fields:       fields,
				})
			}
			if shouldRetryAssignment(assignment, storedStatus) {
				retryDelay := assignmentRetryDelay(assignment)
				retryAt := time.Time{}
				if retryDelay > 0 {
					retryAt = received.Add(retryDelay)
				}
				fields := map[string]string{
					"previous_status": storedStatus,
					"attempt":         strconv.Itoa(assignment.Attempt),
					"max_attempts":    strconv.Itoa(assignment.MaxAttempts),
				}
				if retryDelay > 0 {
					fields["retry_delay"] = retryDelay.String()
					fields["retry_at"] = retryAt.Format(time.RFC3339Nano)
				}
				if assignment.SandboxID != "" {
					fields["sandbox_id"] = assignment.SandboxID
					fields["sandbox_role"] = assignment.SandboxRole
				}
				assignment.Status = "pending"
				assignment.LeasedTo = ""
				assignment.LeaseExpires = time.Time{}
				assignment.RetryAt = retryAt
				assignment.Updated = received
				s.assignments[assignment.ID] = assignment
				s.appendAuditLocked(received, AuditEvent{
					Actor:        "controller",
					Namespace:    assignment.Namespace,
					Action:       "assignment.auto_retry",
					TargetType:   "assignment",
					TargetID:     assignment.ID,
					WorkerID:     id,
					AssignmentID: assignment.ID,
					Status:       assignment.Status,
					Fields:       fields,
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
	if a.Priority < 0 {
		return Assignment{}, fmt.Errorf("assignment priority must be non-negative")
	}
	retryDelay, err := normalizeAssignmentRetryPolicy(a.MaxAttempts, a.RetryDelay, "assignment")
	if err != nil {
		return Assignment{}, err
	}
	workerID := strings.TrimSpace(a.WorkerID)
	policy, imageRef, imageManifestDigest, antiAffinityKey, requiredLabels, requiredCapabilities, resources, err := normalizePlacementFields(a)
	if err != nil {
		return Assignment{}, err
	}
	now := s.now().UTC()
	queueExpires, err := normalizeAssignmentQueueDeadline(now, a.QueueTTL, a.QueueExpires, "assignment")
	if err != nil {
		return Assignment{}, err
	}
	runTimeout, err := normalizeAssignmentRunTimeout(a.RunTimeout, "assignment")
	if err != nil {
		return Assignment{}, err
	}
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
		host, ok := s.hosts[workerID]
		if !ok {
			return Assignment{}, fmt.Errorf("worker %q not registered", workerID)
		}
		if !capabilitiesMatch(host.Capabilities, requiredCapabilities) {
			return Assignment{}, fmt.Errorf("worker %q missing required capabilities", workerID)
		}
	} else if policy != "" || len(requiredLabels) > 0 || len(requiredCapabilities) > 0 {
		selected, err := s.selectWorkerLocked(policy, imageRef, imageManifestDigest, requiredLabels, requiredCapabilities, antiAffinityKey, resources)
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
	a.ImageManifestDigest = imageManifestDigest
	a.ImageDigestRef = strings.TrimSpace(a.ImageDigestRef)
	a.ImagePlatform = strings.TrimSpace(a.ImagePlatform)
	a.AntiAffinityKey = antiAffinityKey
	a.RequiredLabels = requiredLabels
	a.RequiredCapabilities = requiredCapabilities
	a.Resources = resources
	a.Verb = verb
	a.Args = cloneStrings(a.Args)
	a.QueueTTL = ""
	a.QueueExpires = queueExpires
	a.RunTimeout = runTimeout
	a.Attempt = 0
	a.RetryDelay = retryDelay
	a.RetryAt = time.Time{}
	a.Status = "pending"
	a.Created = now
	a.Updated = now
	a.LeasedTo = ""
	a.LeaseExpires = time.Time{}
	a.LastReport = nil
	s.assignments[id] = a
	fields := map[string]string{
		"verb":      verb,
		"policy":    policy,
		"image_ref": imageRef,
	}
	if a.Priority > 0 {
		fields["priority"] = strconv.Itoa(a.Priority)
	}
	if !a.QueueExpires.IsZero() {
		fields["queue_expires"] = a.QueueExpires.Format(time.RFC3339Nano)
	}
	if a.RunTimeout != "" {
		fields["run_timeout"] = a.RunTimeout
	}
	if a.MaxAttempts > 0 {
		fields["max_attempts"] = strconv.Itoa(a.MaxAttempts)
	}
	if a.RetryDelay != "" {
		fields["retry_delay"] = a.RetryDelay
	}
	s.appendAuditLocked(now, AuditEvent{
		Actor:        actor,
		Namespace:    a.Namespace,
		Action:       "assignment.create",
		TargetType:   "assignment",
		TargetID:     id,
		WorkerID:     workerID,
		AssignmentID: id,
		Fields:       fields,
	})
	if err := s.persistLocked(); err != nil {
		return Assignment{}, err
	}
	return cloneAssignment(a), nil
}

func (s *Store) CancelAssignment(id string, req AssignmentCancelRequest) (AssignmentCancelResult, error) {
	return s.CancelAssignmentActor("controller", id, req)
}

func (s *Store) CancelAssignmentActor(actor, id string, req AssignmentCancelRequest) (AssignmentCancelResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return AssignmentCancelResult{}, fmt.Errorf("assignment id required")
	}
	now := s.now().UTC()
	actor = normalizeActor(actor)
	reason := strings.TrimSpace(req.Reason)

	s.mu.Lock()
	defer s.mu.Unlock()
	assignment, ok := s.assignments[id]
	if !ok {
		return AssignmentCancelResult{}, fmt.Errorf("assignment %q not found", id)
	}
	status := normalizeOperationStatus(assignment.Status)
	if !openAssignmentStatus(status) {
		return AssignmentCancelResult{}, fmt.Errorf("assignment %q is %s", id, status)
	}
	if assignment.SandboxID != "" && assignment.SandboxRole == sandboxRoleRun {
		return AssignmentCancelResult{}, fmt.Errorf("assignment %q belongs to hosted sandbox %q; use sandbox stop or delete", id, assignment.SandboxID)
	}
	if !req.Force && (status != "pending" || assignment.LeasedTo != "") {
		return AssignmentCancelResult{}, fmt.Errorf("assignment %q is %s; force required", id, status)
	}

	workerID := assignment.WorkerID
	if workerID == "" {
		workerID = assignment.LeasedTo
	}
	if assignment.WorkerID == "" {
		assignment.WorkerID = workerID
	}
	assignment.Status = "canceled"
	assignment.LeasedTo = ""
	assignment.LeaseExpires = time.Time{}
	assignment.Updated = now
	s.assignments[id] = assignment
	fields := map[string]string{
		"reason":          reason,
		"force":           strconv.FormatBool(req.Force),
		"previous_status": status,
		"operation":       "assignment.cancel",
	}
	if workerID != "" {
		fields["worker_id"] = workerID
	}
	s.appendAuditLocked(now, AuditEvent{
		Actor:        actor,
		Namespace:    assignment.Namespace,
		Action:       "assignment.cancel",
		TargetType:   "assignment",
		TargetID:     id,
		WorkerID:     workerID,
		AssignmentID: id,
		Status:       assignment.Status,
		Fields:       fields,
	})
	if err := s.persistLocked(); err != nil {
		return AssignmentCancelResult{}, err
	}
	return AssignmentCancelResult{
		Assignment:     cloneAssignment(assignment),
		Reason:         reason,
		Force:          req.Force,
		Canceled:       true,
		PreviousStatus: status,
	}, nil
}

func (s *Store) RetryAssignment(id string, req AssignmentRetryRequest) (AssignmentRetryResult, error) {
	return s.RetryAssignmentActor("controller", id, req)
}

func (s *Store) RetryAssignmentActor(actor, id string, req AssignmentRetryRequest) (AssignmentRetryResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return AssignmentRetryResult{}, fmt.Errorf("assignment id required")
	}
	now := s.now().UTC()
	actor = normalizeActor(actor)
	reason := strings.TrimSpace(req.Reason)
	requestedWorkerID := strings.TrimSpace(req.WorkerID)

	s.mu.Lock()
	defer s.mu.Unlock()
	assignment, ok := s.assignments[id]
	if !ok {
		return AssignmentRetryResult{}, fmt.Errorf("assignment %q not found", id)
	}
	status := normalizeOperationStatus(assignment.Status)
	if openAssignmentStatus(status) {
		return AssignmentRetryResult{}, fmt.Errorf("assignment %q is %s", id, status)
	}
	if assignment.SandboxID != "" || assignment.SandboxRole != "" {
		return AssignmentRetryResult{}, fmt.Errorf("assignment %q belongs to hosted sandbox %q; use sandbox start or restart", id, assignment.SandboxID)
	}
	if assignment.WarmPool != "" || assignment.WarmPoolSlot != "" {
		return AssignmentRetryResult{}, fmt.Errorf("assignment %q belongs to warm pool; use warm-pool reconcile", id)
	}

	previousWorkerID := assignment.WorkerID
	replanned := false
	switch {
	case requestedWorkerID != "":
		host, ok := s.hosts[requestedWorkerID]
		if !ok {
			return AssignmentRetryResult{}, fmt.Errorf("worker %q not registered", requestedWorkerID)
		}
		if !capabilitiesMatch(host.Capabilities, assignment.RequiredCapabilities) {
			return AssignmentRetryResult{}, fmt.Errorf("worker %q missing required capabilities", requestedWorkerID)
		}
		assignment.WorkerID = requestedWorkerID
		replanned = requestedWorkerID != previousWorkerID
	case req.Replan:
		if !assignmentCanPlace(assignment) {
			return AssignmentRetryResult{}, fmt.Errorf("assignment %q cannot be replanned", id)
		}
		selected, err := s.selectWorkerLocked(assignmentPolicy(assignment), assignment.ImageRef, assignment.ImageManifestDigest, assignment.RequiredLabels, assignment.RequiredCapabilities, assignment.AntiAffinityKey, assignment.Resources)
		if err != nil {
			return AssignmentRetryResult{}, err
		}
		assignment.WorkerID = selected
		replanned = selected != previousWorkerID
	case assignment.WorkerID != "":
		host, ok := s.hosts[assignment.WorkerID]
		if !ok {
			return AssignmentRetryResult{}, fmt.Errorf("worker %q not registered; set replan or worker_id", assignment.WorkerID)
		}
		if !capabilitiesMatch(host.Capabilities, assignment.RequiredCapabilities) {
			return AssignmentRetryResult{}, fmt.Errorf("worker %q missing required capabilities", assignment.WorkerID)
		}
	}

	assignment.Status = "pending"
	assignment.LeasedTo = ""
	assignment.LeaseExpires = time.Time{}
	assignment.QueueTTL = ""
	assignment.QueueExpires = time.Time{}
	assignment.RetryAt = time.Time{}
	assignment.LastReport = nil
	assignment.Updated = now
	s.assignments[id] = assignment
	fields := map[string]string{
		"reason":          reason,
		"previous_status": status,
		"replan":          strconv.FormatBool(req.Replan),
	}
	if previousWorkerID != "" {
		fields["previous_worker_id"] = previousWorkerID
	}
	if assignment.WorkerID != "" {
		fields["worker_id"] = assignment.WorkerID
	}
	s.appendAuditLocked(now, AuditEvent{
		Actor:        actor,
		Namespace:    assignment.Namespace,
		Action:       "assignment.retry",
		TargetType:   "assignment",
		TargetID:     id,
		WorkerID:     assignment.WorkerID,
		AssignmentID: id,
		Status:       assignment.Status,
		Fields:       fields,
	})
	if err := s.persistLocked(); err != nil {
		return AssignmentRetryResult{}, err
	}
	return AssignmentRetryResult{
		Assignment:       cloneAssignment(assignment),
		Reason:           reason,
		PreviousStatus:   status,
		PreviousWorkerID: previousWorkerID,
		Replanned:        replanned,
	}, nil
}

func (s *Store) PlanAssignment(a Assignment, limit int) (PlacementPlan, error) {
	policy, imageRef, imageManifestDigest, antiAffinityKey, requiredLabels, requiredCapabilities, resources, err := normalizePlacementFields(a)
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
	s.reconcileLocked(now)
	candidates, skipped := s.placementEvaluationLocked(policy, imageRef, imageManifestDigest, requiredLabels, requiredCapabilities, antiAffinityKey, resources)
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	plan := PlacementPlan{
		ID:                   s.nextPlacementPlanIDLocked(now),
		Created:              now,
		Namespace:            normalizeNamespace(a.Namespace),
		Policy:               policy,
		ImageRef:             imageRef,
		ImageManifestDigest:  imageManifestDigest,
		ImageDigestRef:       strings.TrimSpace(a.ImageDigestRef),
		ImagePlatform:        strings.TrimSpace(a.ImagePlatform),
		RequiredLabels:       cloneLabels(requiredLabels),
		RequiredCapabilities: cloneStrings(requiredCapabilities),
		AntiAffinityKey:      antiAffinityKey,
		Resources:            normalizeResources(resources),
		Limit:                limit,
		Candidates:           clonePlacementCandidates(candidates),
		Skipped:              clonePlacementSkips(skipped),
	}
	s.plans = append(s.plans, clonePlacementPlan(plan))
	if err := s.persistLocked(); err != nil {
		return plan, err
	}
	return plan, nil
}

func (s *Store) GetPlacementPlan(id string) (PlacementPlan, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return PlacementPlan{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, plan := range s.plans {
		if plan.ID == id {
			return clonePlacementPlan(plan), true
		}
	}
	return PlacementPlan{}, false
}

func (s *Store) ListPlacementPlansPage(filter PlacementPlanListFilter) PlacementPlanListResult {
	filter.Namespace = normalizeNamespace(filter.Namespace)
	filter.Policy = strings.TrimSpace(filter.Policy)
	filter.ImageRef = strings.TrimSpace(filter.ImageRef)
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	if filter.Limit < 0 {
		filter.Limit = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	plans := s.sortedPlacementPlansLocked()
	filtered := plans[:0]
	for _, plan := range plans {
		if !namespaceMatches(plan.Namespace, filter.Namespace) {
			continue
		}
		if filter.Policy != "" && plan.Policy != filter.Policy {
			continue
		}
		if filter.ImageRef != "" && plan.ImageRef != filter.ImageRef {
			continue
		}
		filtered = append(filtered, plan)
	}
	result := PlacementPlanListResult{Offset: filter.Offset, Limit: filter.Limit}
	if filter.Offset >= len(filtered) {
		return result
	}
	end := len(filtered) - filter.Offset
	start := 0
	if filter.Limit > 0 && end > filter.Limit {
		start = end - filter.Limit
		result.NextOffset = filter.Offset + filter.Limit
	}
	result.Plans = clonePlacementPlans(filtered[start:end])
	result.Count = len(result.Plans)
	return result
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
		ID:                   s.nextAssignmentIDLocked(now),
		Namespace:            pool.Namespace,
		WorkerID:             slot.WorkerID,
		WarmPoolSlot:         slot.ID,
		RequiredCapabilities: cloneStrings(slot.RequiredCapabilities),
		Verb:                 "cove",
		Args:                 warmPoolClaimArgs(vmName, command, env),
		Status:               "pending",
		Created:              now,
		Updated:              now,
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
	if strings.TrimSpace(req.ManifestBundle) != "" {
		return SandboxStatus{}, fmt.Errorf("sandbox manifest_bundle must be resolved before store admission")
	}
	if imageRef == "" {
		return SandboxStatus{}, fmt.Errorf("sandbox image_ref required")
	}
	if req.MaxActiveSandboxes < 0 {
		return SandboxStatus{}, fmt.Errorf("sandbox max_active_sandboxes must be non-negative")
	}
	if req.Priority < 0 {
		return SandboxStatus{}, fmt.Errorf("sandbox priority must be non-negative")
	}
	retryDelay, err := normalizeAssignmentRetryPolicy(req.MaxAttempts, req.RetryDelay, "sandbox")
	if err != nil {
		return SandboxStatus{}, err
	}
	runTimeout, err := normalizeAssignmentRunTimeout(req.RunTimeout, "sandbox")
	if err != nil {
		return SandboxStatus{}, err
	}
	args := cloneStrings(req.Args)
	if err := validateForkRunArgs(args, "sandbox"); err != nil {
		return SandboxStatus{}, err
	}
	namespace := normalizeNamespace(req.Namespace)
	assignment := Assignment{
		Namespace:            namespace,
		Policy:               strings.TrimSpace(req.Policy),
		ImageRef:             imageRef,
		ImageManifestDigest:  strings.TrimSpace(req.ImageManifestDigest),
		ImageDigestRef:       strings.TrimSpace(req.ImageDigestRef),
		ImagePlatform:        strings.TrimSpace(req.ImagePlatform),
		RequiredLabels:       cloneLabels(req.RequiredLabels),
		RequiredCapabilities: sortedUniqueStrings(req.RequiredCapabilities),
		AntiAffinityKey:      strings.TrimSpace(req.AntiAffinityKey),
		Resources:            req.Resources,
		Priority:             req.Priority,
		RunTimeout:           runTimeout,
		MaxAttempts:          req.MaxAttempts,
		RetryDelay:           retryDelay,
	}
	policy, imageRef, imageManifestDigest, antiAffinityKey, requiredLabels, requiredCapabilities, resources, err := normalizePlacementFields(assignment)
	if err != nil {
		return SandboxStatus{}, err
	}
	if policy == "" {
		policy = PolicyImageAffinity
	}
	now := s.now().UTC()
	queueExpires, err := normalizeAssignmentQueueDeadline(now, req.QueueTTL, req.QueueExpires, "sandbox")
	if err != nil {
		return SandboxStatus{}, err
	}
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
	if req.MaxActiveSandboxes > 0 {
		active := s.activeSandboxCountLocked(namespace)
		if active >= req.MaxActiveSandboxes {
			return SandboxStatus{}, fmt.Errorf("sandbox namespace %q has %d active sandboxes, max_active_sandboxes is %d", namespace, active, req.MaxActiveSandboxes)
		}
	}
	workerID, err := s.selectWorkerLocked(policy, imageRef, imageManifestDigest, requiredLabels, requiredCapabilities, antiAffinityKey, resources)
	if err != nil {
		return SandboxStatus{}, err
	}
	vmName := strings.TrimSpace(req.VMName)
	if vmName == "" {
		vmName = sandboxVMName(id)
	}
	assignment = Assignment{
		ID:                   id,
		Namespace:            namespace,
		WorkerID:             workerID,
		SandboxID:            id,
		SandboxRole:          sandboxRoleRun,
		Policy:               policy,
		ImageRef:             imageRef,
		ImageManifestDigest:  imageManifestDigest,
		ImageDigestRef:       strings.TrimSpace(req.ImageDigestRef),
		ImagePlatform:        strings.TrimSpace(req.ImagePlatform),
		RequiredLabels:       requiredLabels,
		RequiredCapabilities: requiredCapabilities,
		AntiAffinityKey:      antiAffinityKey,
		Resources:            normalizeResources(resources),
		Priority:             req.Priority,
		QueueExpires:         queueExpires,
		RunTimeout:           runTimeout,
		MaxAttempts:          req.MaxAttempts,
		RetryDelay:           retryDelay,
		Verb:                 "cove",
		Args:                 sandboxRunArgs(imageRef, vmName, args),
		Status:               "pending",
		Created:              now,
		Updated:              now,
	}
	s.assignments[id] = assignment
	fields := map[string]string{
		"image_ref": imageRef,
		"vm_name":   vmName,
		"policy":    policy,
	}
	if req.Priority > 0 {
		fields["priority"] = strconv.Itoa(req.Priority)
	}
	if !queueExpires.IsZero() {
		fields["queue_expires"] = queueExpires.Format(time.RFC3339Nano)
	}
	if runTimeout != "" {
		fields["run_timeout"] = runTimeout
	}
	if req.MaxAttempts > 0 {
		fields["max_attempts"] = strconv.Itoa(req.MaxAttempts)
	}
	if retryDelay != "" {
		fields["retry_delay"] = retryDelay
	}
	s.appendAuditLocked(now, AuditEvent{
		Actor:        actor,
		Namespace:    assignment.Namespace,
		Action:       "sandbox.create",
		TargetType:   "sandbox",
		TargetID:     id,
		WorkerID:     workerID,
		AssignmentID: id,
		Fields:       fields,
	})
	if err := s.persistLocked(); err != nil {
		return SandboxStatus{}, err
	}
	return s.sandboxStatusLocked(assignment, now), nil
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
	return s.sandboxStatusLocked(assignment, s.now().UTC()), true
}

func (s *Store) ListSandboxes() []SandboxStatus {
	return s.ListSandboxesNamespace("")
}

func (s *Store) ListSandboxesNamespace(namespace string) []SandboxStatus {
	return s.ListSandboxesFiltered(SandboxListFilter{Namespace: namespace})
}

func (s *Store) ListSandboxesFiltered(filter SandboxListFilter) []SandboxStatus {
	return s.ListSandboxesPage(filter).Sandboxes
}

func (s *Store) ListSandboxesPage(filter SandboxListFilter) SandboxListResult {
	filter.Namespace = normalizeNamespace(filter.Namespace)
	filter.Status = strings.TrimSpace(filter.Status)
	filter.WorkerID = strings.TrimSpace(filter.WorkerID)
	filter.ImageRef = strings.TrimSpace(filter.ImageRef)
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	if filter.Limit < 0 {
		filter.Limit = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	result := SandboxListResult{Offset: filter.Offset, Limit: filter.Limit}
	offset := 0
	for _, assignment := range s.sortedAssignmentsLocked() {
		if assignment.SandboxID == "" || assignment.SandboxRole != sandboxRoleRun {
			continue
		}
		if !namespaceMatches(assignment.Namespace, filter.Namespace) {
			continue
		}
		sandbox := s.sandboxStatusLocked(assignment, now)
		if filter.Status != "" && sandbox.Status != filter.Status {
			continue
		}
		if filter.WorkerID != "" && sandbox.WorkerID != filter.WorkerID {
			continue
		}
		if filter.ImageRef != "" && sandbox.ImageRef != filter.ImageRef {
			continue
		}
		if offset < filter.Offset {
			offset++
			continue
		}
		if filter.Limit > 0 && len(result.Sandboxes) >= filter.Limit {
			result.NextOffset = filter.Offset + len(result.Sandboxes)
			break
		}
		result.Sandboxes = append(result.Sandboxes, sandbox)
	}
	result.Count = len(result.Sandboxes)
	return result
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

func (s *Store) ListWorkerMetering(namespace, workerID, sandboxID, status string) SandboxMeteringResult {
	namespace = normalizeNamespace(namespace)
	workerID = strings.TrimSpace(workerID)
	sandboxID = strings.TrimSpace(sandboxID)
	status = strings.TrimSpace(status)
	s.mu.Lock()
	defer s.mu.Unlock()
	records := make([]SandboxMeteringRecord, 0, len(s.metering))
	for _, record := range s.sortedMeteringLocked() {
		if !namespaceMatches(record.Namespace, namespace) {
			continue
		}
		if workerID != "" && record.WorkerID != workerID {
			continue
		}
		if sandboxID != "" && record.SandboxID != sandboxID {
			continue
		}
		if status != "" && record.Status != status {
			continue
		}
		records = append(records, cloneSandboxMeteringRecord(record))
	}
	summary := sandboxMeteringSummary(namespace, sandboxID, records)
	summary.WorkerID = workerID
	return SandboxMeteringResult{
		Records: records,
		Summary: summary,
	}
}

func (s *Store) ListAssignmentMetering(namespace, assignmentID, status string) SandboxMeteringResult {
	namespace = normalizeNamespace(namespace)
	assignmentID = strings.TrimSpace(assignmentID)
	status = strings.TrimSpace(status)
	s.mu.Lock()
	defer s.mu.Unlock()
	records := make([]SandboxMeteringRecord, 0, len(s.metering))
	for _, record := range s.sortedMeteringLocked() {
		if !namespaceMatches(record.Namespace, namespace) {
			continue
		}
		if assignmentID != "" && record.AssignmentID != assignmentID {
			continue
		}
		if status != "" && record.Status != status {
			continue
		}
		records = append(records, cloneSandboxMeteringRecord(record))
	}
	summary := sandboxMeteringSummary(namespace, "", records)
	summary.AssignmentID = assignmentID
	return SandboxMeteringResult{
		Records: records,
		Summary: summary,
	}
}

func (s *Store) ListSandboxReportsPage(filter SandboxReportFilter) SandboxReportListResult {
	filter.Namespace = normalizeNamespace(filter.Namespace)
	filter.SandboxID = strings.TrimSpace(filter.SandboxID)
	filter.Role = strings.TrimSpace(filter.Role)
	filter.Status = strings.TrimSpace(filter.Status)
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	if filter.Limit < 0 {
		filter.Limit = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	reports := make([]SandboxReport, 0)
	for _, assignment := range s.sortedAssignmentsLocked() {
		if assignment.LastReport == nil {
			continue
		}
		if assignment.SandboxID == "" {
			continue
		}
		if !namespaceMatches(assignment.Namespace, filter.Namespace) {
			continue
		}
		if filter.SandboxID != "" && assignment.SandboxID != filter.SandboxID {
			continue
		}
		if filter.Role != "" && assignment.SandboxRole != filter.Role {
			continue
		}
		if filter.Status != "" && assignment.Status != filter.Status {
			continue
		}
		reports = append(reports, sandboxReportFromAssignment(assignment))
	}
	result := SandboxReportListResult{Offset: filter.Offset, Limit: filter.Limit}
	if filter.Offset >= len(reports) {
		return result
	}
	end := len(reports) - filter.Offset
	start := 0
	if filter.Limit > 0 && end > filter.Limit {
		start = end - filter.Limit
		result.NextOffset = filter.Offset + filter.Limit
	}
	result.Reports = reports[start:end]
	result.Count = len(result.Reports)
	return result
}

func (s *Store) ListAssignmentReportsPage(filter AssignmentReportFilter) AssignmentReportListResult {
	filter.Namespace = normalizeNamespace(filter.Namespace)
	filter.AssignmentID = strings.TrimSpace(filter.AssignmentID)
	filter.WorkerID = strings.TrimSpace(filter.WorkerID)
	filter.Status = strings.TrimSpace(filter.Status)
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	if filter.Limit < 0 {
		filter.Limit = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	reports := make([]AssignmentReport, 0)
	for _, report := range s.sortedAssignmentReportsLocked() {
		if !namespaceMatches(report.Namespace, filter.Namespace) {
			continue
		}
		if filter.AssignmentID != "" && report.AssignmentID != filter.AssignmentID {
			continue
		}
		if filter.WorkerID != "" && report.WorkerID != filter.WorkerID {
			continue
		}
		if filter.Status != "" && report.Status != filter.Status {
			continue
		}
		reports = append(reports, cloneAssignmentReport(report))
	}
	result := AssignmentReportListResult{Offset: filter.Offset, Limit: filter.Limit}
	if filter.Offset >= len(reports) {
		return result
	}
	end := len(reports) - filter.Offset
	start := 0
	if filter.Limit > 0 && end > filter.Limit {
		start = end - filter.Limit
		result.NextOffset = filter.Offset + filter.Limit
	}
	result.Reports = reports[start:end]
	result.Count = len(result.Reports)
	return result
}

func (s *Store) OperationsSummary(namespace string) OperationsSummary {
	namespace = normalizeNamespace(namespace)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	summary := OperationsSummary{
		Time:      now,
		Namespace: namespace,
		Workers: WorkerOperationsSummary{
			ByStatus: make(map[string]int),
		},
		Assignments: AssignmentOperationsSummary{
			ByStatus: make(map[string]int),
		},
		Sandboxes: SandboxOperationsSummary{
			ByStatus: make(map[string]int),
		},
		WarmPools: WarmPoolOperationsSummary{
			ByStatus: make(map[string]int),
		},
	}
	capabilityCoverage := make(map[string]*WorkerCapabilitySummary)

	for _, host := range s.sortedHostsLocked() {
		host = s.statusLocked(host)
		summary.Workers.Total++
		addStatusCount(summary.Workers.ByStatus, host.Status)
		addCapabilityCoverage(capabilityCoverage, host)
		switch host.Status {
		case "ready":
			summary.Workers.Ready++
		case "cordoned":
			summary.Workers.Cordoned++
			summary.Workers.Attention = append(summary.Workers.Attention, host)
		case "quarantined":
			summary.Workers.Quarantined++
			summary.Workers.Attention = append(summary.Workers.Attention, host)
		case "stale":
			summary.Workers.Stale++
			summary.Workers.Attention = append(summary.Workers.Attention, host)
		default:
			summary.Workers.Attention = append(summary.Workers.Attention, host)
		}
	}
	summary.Workers.Capabilities = sortedCapabilityCoverage(capabilityCoverage)

	for _, pool := range s.sortedWarmPoolsLocked() {
		if !namespaceMatches(pool.Namespace, namespace) {
			continue
		}
		summary.WarmPools.Total++
		summary.WarmPools.Desired += pool.Size
		summary.WarmPools.Pools = append(summary.WarmPools.Pools, s.warmPoolStatusLocked(pool.Name))
	}

	var metering []SandboxMeteringRecord
	for _, assignment := range s.sortedAssignmentsLocked() {
		if !namespaceMatches(assignment.Namespace, namespace) {
			continue
		}
		status := normalizeOperationStatus(assignment.Status)
		summary.Assignments.Total++
		addStatusCount(summary.Assignments.ByStatus, status)
		if openAssignmentStatus(status) {
			summary.Assignments.Active++
			summary.Assignments.ActiveAssignments = append(summary.Assignments.ActiveAssignments, cloneAssignment(assignment))
		} else {
			summary.Assignments.Terminal++
		}

		if assignment.SandboxID != "" && assignment.SandboxRole == sandboxRoleRun {
			sandbox := s.sandboxStatusLocked(assignment, now)
			sandbox.Status = status
			summary.Sandboxes.Total++
			addStatusCount(summary.Sandboxes.ByStatus, status)
			if sandboxTerminalStatus(status) {
				summary.Sandboxes.Terminal++
			} else {
				summary.Sandboxes.Active++
				summary.Sandboxes.ActiveSandboxes = append(summary.Sandboxes.ActiveSandboxes, sandbox)
			}
			if status == "draining" {
				summary.Sandboxes.DrainingSandboxes = append(summary.Sandboxes.DrainingSandboxes, sandbox)
			}
		}

		if assignment.WarmPool != "" {
			addStatusCount(summary.WarmPools.ByStatus, status)
			if activeAssignmentStatus(status) {
				summary.WarmPools.Active++
			}
			if openAssignmentStatus(status) {
				summary.WarmPools.Slots++
			} else {
				summary.WarmPools.Terminal++
			}
			switch status {
			case "ready":
				summary.WarmPools.Ready++
			case "claimed":
				summary.WarmPools.Claimed++
			case "draining":
				summary.WarmPools.Draining++
			}
		}
	}
	for _, record := range s.sortedMeteringLocked() {
		if !namespaceMatches(record.Namespace, namespace) {
			continue
		}
		metering = append(metering, record)
	}
	summary.Metering = sandboxMeteringSummary(namespace, "", metering)
	return summary
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
	runTimeout, err := normalizeAssignmentRunTimeout(req.Timeout, "sandbox exec")
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
	if err := requireSandboxLeaseHolder(now, id, &sandbox, req.Holder); err != nil {
		return SandboxExecResult{}, err
	}
	if sandbox.Status != "ready" {
		return SandboxExecResult{}, fmt.Errorf("sandbox %q is %s", id, sandbox.Status)
	}
	if err := s.requireSandboxWorkerReadyLocked(now, sandbox.WorkerID, sandbox.RequiredCapabilities); err != nil {
		return SandboxExecResult{}, err
	}
	vmName := SandboxAssignmentVMName(sandbox)
	assignment := Assignment{
		ID:          s.nextAssignmentIDLocked(now),
		Namespace:   sandbox.Namespace,
		WorkerID:    sandbox.WorkerID,
		SandboxID:   id,
		SandboxRole: sandboxRoleExec,
		Priority:    sandbox.Priority,
		RunTimeout:  runTimeout,
		Verb:        "cove",
		Args:        sandboxExecArgs(vmName, command, env),
		Status:      "pending",
		Created:     now,
		Updated:     now,
	}
	s.assignments[assignment.ID] = assignment
	fields := map[string]string{
		"vm_name": vmName,
		"argc":    strconv.Itoa(len(command)),
	}
	if runTimeout != "" {
		fields["run_timeout"] = runTimeout
	}
	s.appendAuditLocked(now, AuditEvent{
		Actor:        actor,
		Namespace:    sandbox.Namespace,
		Action:       "sandbox.exec",
		TargetType:   "sandbox",
		TargetID:     id,
		WorkerID:     sandbox.WorkerID,
		AssignmentID: assignment.ID,
		Fields:       fields,
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
	runTimeout, err := normalizeAssignmentRunTimeout(req.Timeout, "sandbox control")
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
	if err := requireSandboxLeaseHolder(now, id, &sandbox, req.Holder); err != nil {
		return SandboxControlResult{}, err
	}
	if sandbox.Status != "ready" {
		return SandboxControlResult{}, fmt.Errorf("sandbox %q is %s", id, sandbox.Status)
	}
	if err := s.requireSandboxWorkerReadyLocked(now, sandbox.WorkerID, sandbox.RequiredCapabilities); err != nil {
		return SandboxControlResult{}, err
	}
	vmName := SandboxAssignmentVMName(sandbox)
	assignment := Assignment{
		ID:          s.nextAssignmentIDLocked(now),
		Namespace:   sandbox.Namespace,
		WorkerID:    sandbox.WorkerID,
		SandboxID:   id,
		SandboxRole: sandboxRoleControl,
		Priority:    sandbox.Priority,
		RunTimeout:  runTimeout,
		Verb:        "cove-control",
		Args:        []string{vmName, string(payload)},
		Status:      "pending",
		Created:     now,
		Updated:     now,
	}
	s.assignments[assignment.ID] = assignment
	fields := map[string]string{
		"vm_name": vmName,
		"type":    typ,
	}
	if runTimeout != "" {
		fields["run_timeout"] = runTimeout
	}
	s.appendAuditLocked(now, AuditEvent{
		Actor:        actor,
		Namespace:    sandbox.Namespace,
		Action:       "sandbox.control",
		TargetType:   "sandbox",
		TargetID:     id,
		WorkerID:     sandbox.WorkerID,
		AssignmentID: assignment.ID,
		Fields:       fields,
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
	return SandboxLeaseResult{Sandbox: s.sandboxStatusLocked(assignment, now), Lease: lease}, nil
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
		return s.sandboxStatusLocked(assignment, now), nil
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
	return s.sandboxStatusLocked(assignment, now), nil
}

func (s *Store) StartSandbox(id string) (SandboxStartResult, error) {
	return s.StartSandboxActor("controller", id)
}

func (s *Store) StartSandboxActor(actor, id string, reqs ...SandboxMutationRequest) (SandboxStartResult, error) {
	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.startSandboxLocked(now, normalizeActor(actor), id, "sandbox.start", sandboxMutationHolder(reqs))
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

func (s *Store) RestartSandboxActor(actor, id string, reqs ...SandboxMutationRequest) (SandboxRestartResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return SandboxRestartResult{}, fmt.Errorf("sandbox id required")
	}
	now := s.now().UTC()
	actor = normalizeActor(actor)
	holder := sandboxMutationHolder(reqs)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reconcileLocked(now)
	assignment, ok := s.sandboxRunAssignmentLocked(id)
	if !ok {
		return SandboxRestartResult{}, fmt.Errorf("sandbox %q not found", id)
	}
	if err := requireSandboxLeaseHolder(now, id, &assignment, holder); err != nil {
		return SandboxRestartResult{}, err
	}
	if sandboxTerminalStatus(assignment.Status) {
		started, err := s.startSandboxLocked(now, actor, id, "sandbox.restart", holder)
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
		result.CanceledAssignments = s.cancelSandboxWorkLocked(now, actor, id, "sandbox.restart")
		cleanup, ok := s.activeSandboxCleanupLocked(id)
		if !ok {
			cleanup = Assignment{
				ID:          s.nextAssignmentIDLocked(now),
				Namespace:   assignment.Namespace,
				WorkerID:    assignment.WorkerID,
				SandboxID:   id,
				SandboxRole: sandboxRoleStop,
				Priority:    assignment.Priority,
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
	fields := map[string]string{
		"vm_name":    vmName,
		"status":     result.Status,
		"restarting": strconv.FormatBool(result.Restarting),
		"cleanup":    strconv.FormatBool(result.Cleanup != nil),
	}
	if len(result.CanceledAssignments) > 0 {
		fields["canceled_assignments"] = strings.Join(result.CanceledAssignments, ",")
	}
	s.appendAuditLocked(now, AuditEvent{
		Actor:        actor,
		Namespace:    assignment.Namespace,
		Action:       "sandbox.restart",
		TargetType:   "sandbox",
		TargetID:     id,
		WorkerID:     assignment.WorkerID,
		AssignmentID: assignment.ID,
		Fields:       fields,
	})
	if err := s.persistLocked(); err != nil {
		return SandboxRestartResult{}, err
	}
	return result, nil
}

func (s *Store) DeleteSandbox(id string) (SandboxDeleteResult, error) {
	return s.DeleteSandboxActor("controller", id)
}

func (s *Store) DeleteSandboxActor(actor, id string, reqs ...SandboxMutationRequest) (SandboxDeleteResult, error) {
	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.stopSandboxLocked(now, normalizeActor(actor), id, "sandbox.delete", sandboxMutationHolder(reqs))
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

func (s *Store) StopSandboxActor(actor, id string, reqs ...SandboxMutationRequest) (SandboxStopResult, error) {
	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.stopSandboxLocked(now, normalizeActor(actor), id, "sandbox.stop", sandboxMutationHolder(reqs))
	if err != nil {
		return SandboxStopResult{}, err
	}
	if err := s.persistLocked(); err != nil {
		return SandboxStopResult{}, err
	}
	return result, nil
}

func (s *Store) WaitSandbox(id string) (SandboxWaitResult, error) {
	return s.WaitSandboxStatus(id, "")
}

func (s *Store) WaitSandboxStatus(id, targetStatus string) (SandboxWaitResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return SandboxWaitResult{}, fmt.Errorf("sandbox id required")
	}
	targetStatus = strings.TrimSpace(targetStatus)
	s.mu.Lock()
	defer s.mu.Unlock()
	assignment, ok := s.sandboxRunAssignmentLocked(id)
	if !ok {
		return SandboxWaitResult{}, fmt.Errorf("sandbox %q not found", id)
	}
	sandbox := s.sandboxStatusLocked(assignment, s.now().UTC())
	return SandboxWaitResult{
		Done:         sandboxWaitDone(sandbox.Status, targetStatus),
		TargetStatus: targetStatus,
		Sandbox:      sandbox,
	}, nil
}

func (s *Store) PrepareImage(req ImagePrepareRequest) (ImagePrepareResult, error) {
	return s.PrepareImageActor("controller", req)
}

func (s *Store) PrepareImageActor(actor string, req ImagePrepareRequest) (ImagePrepareResult, error) {
	sourceRef := strings.TrimSpace(req.SourceRef)
	imageRef := strings.TrimSpace(req.ImageRef)
	if strings.TrimSpace(req.ManifestBundle) != "" {
		return ImagePrepareResult{}, fmt.Errorf("image prepare manifest_bundle must be resolved before store admission")
	}
	if sourceRef == "" {
		return ImagePrepareResult{}, fmt.Errorf("image prepare source_ref required")
	}
	if imageRef == "" {
		return ImagePrepareResult{}, fmt.Errorf("image prepare image_ref required")
	}
	namespace := normalizeNamespace(req.Namespace)
	labels := cloneLabels(req.RequiredLabels)
	capabilities := sortedUniqueStrings(req.RequiredCapabilities)
	now := s.now().UTC()
	actor = normalizeActor(actor)

	s.mu.Lock()
	defer s.mu.Unlock()
	if !req.DryRun {
		s.reconcileLocked(now)
	}
	result := ImagePrepareResult{
		ID:                   s.nextImagePrepareIDLocked(now),
		Created:              now,
		Namespace:            namespace,
		SourceRef:            sourceRef,
		ImageRef:             imageRef,
		ImageManifestDigest:  strings.TrimSpace(req.ImageManifestDigest),
		ImageDigestRef:       strings.TrimSpace(req.ImageDigestRef),
		ImagePlatform:        strings.TrimSpace(req.ImagePlatform),
		RequiredLabels:       labels,
		RequiredCapabilities: capabilities,
		DryRun:               req.DryRun,
	}
	for _, host := range s.sortedHostsLocked() {
		host = s.statusLocked(host)
		if missing := missingLabels(host.Labels, labels); len(missing) > 0 {
			result.Skipped = append(result.Skipped, ImagePrepareSkip{
				WorkerID:      host.ID,
				Reason:        "label",
				MissingLabels: missing,
			})
			continue
		}
		if missing := missingCapabilities(host.Capabilities, capabilities); len(missing) > 0 {
			result.Skipped = append(result.Skipped, ImagePrepareSkip{
				WorkerID:            host.ID,
				Reason:              "capability",
				MissingCapabilities: missing,
			})
			continue
		}
		if host.Status != "ready" {
			result.Skipped = append(result.Skipped, ImagePrepareSkip{WorkerID: host.ID, Reason: "status", Status: host.Status})
			continue
		}
		if workerHasImage(host, imageRef, result.ImageManifestDigest) {
			result.Skipped = append(result.Skipped, ImagePrepareSkip{WorkerID: host.ID, Reason: "present"})
			continue
		}
		if s.activeImagePrepareLocked(host.ID, imageRef) {
			result.Skipped = append(result.Skipped, ImagePrepareSkip{WorkerID: host.ID, Reason: "active"})
			continue
		}
		force := req.Force || (result.ImageManifestDigest != "" && containsString(host.ImageRefs, imageRef))
		id := s.nextAssignmentIDLocked(now)
		if req.DryRun {
			id = fmt.Sprintf("planned-%s-%d", id, len(result.Assignments)+1)
		}
		assignment := Assignment{
			ID:                   id,
			Namespace:            namespace,
			WorkerID:             host.ID,
			ImageRef:             imageRef,
			ImageManifestDigest:  result.ImageManifestDigest,
			ImageDigestRef:       result.ImageDigestRef,
			ImagePlatform:        result.ImagePlatform,
			RequiredLabels:       labels,
			RequiredCapabilities: capabilities,
			Verb:                 "cove",
			Args:                 imagePrepareArgs(sourceRef, imageRef, force),
			Status:               "pending",
			Created:              now,
			Updated:              now,
		}
		if !req.DryRun {
			s.assignments[assignment.ID] = assignment
		}
		result.Assignments = append(result.Assignments, cloneAssignment(assignment))
	}
	if len(result.Assignments) == 0 && len(result.Skipped) == 0 {
		return result, fmt.Errorf("no workers match image prepare")
	}
	result = normalizeImagePrepareResult(result)
	if req.DryRun {
		return result, nil
	}
	s.preparations = append(s.preparations, cloneImagePrepareResult(result))
	if len(result.Assignments) > 0 {
		fields := map[string]string{
			"source_ref":  sourceRef,
			"assignments": strconv.Itoa(len(result.Assignments)),
		}
		if result.ImageManifestDigest != "" {
			fields["image_digest"] = result.ImageManifestDigest
		}
		s.appendAuditLocked(now, AuditEvent{
			Actor:      actor,
			Namespace:  namespace,
			Action:     "image.prepare",
			TargetType: "image",
			TargetID:   imageRef,
			Fields:     fields,
		})
	}
	if err := s.persistLocked(); err != nil {
		return result, err
	}
	return result, nil
}

func (s *Store) GetImagePreparation(id string) (ImagePrepareResult, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ImagePrepareResult{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, prep := range s.preparations {
		if prep.ID == id {
			return cloneImagePrepareResult(prep), true
		}
	}
	return ImagePrepareResult{}, false
}

func (s *Store) ListImagePreparationsPage(filter ImagePrepareListFilter) ImagePrepareListResult {
	filter.Namespace = normalizeNamespace(filter.Namespace)
	filter.SourceRef = strings.TrimSpace(filter.SourceRef)
	filter.ImageRef = strings.TrimSpace(filter.ImageRef)
	filter.ImageManifestDigest = strings.TrimSpace(filter.ImageManifestDigest)
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	if filter.Limit < 0 {
		filter.Limit = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	preps := s.sortedImagePreparationsLocked()
	filtered := preps[:0]
	for _, prep := range preps {
		if !namespaceMatches(prep.Namespace, filter.Namespace) {
			continue
		}
		if filter.SourceRef != "" && prep.SourceRef != filter.SourceRef {
			continue
		}
		if filter.ImageRef != "" && prep.ImageRef != filter.ImageRef {
			continue
		}
		if filter.ImageManifestDigest != "" && prep.ImageManifestDigest != filter.ImageManifestDigest {
			continue
		}
		filtered = append(filtered, prep)
	}
	result := ImagePrepareListResult{Offset: filter.Offset, Limit: filter.Limit}
	if filter.Offset >= len(filtered) {
		return result
	}
	end := len(filtered) - filter.Offset
	start := 0
	if filter.Limit > 0 && end > filter.Limit {
		start = end - filter.Limit
		result.NextOffset = filter.Offset + filter.Limit
	}
	result.Preparations = cloneImagePrepareResults(filtered[start:end])
	result.Count = len(result.Preparations)
	return result
}

func (s *Store) PushImageGC(req ImageGCRequest) (ImageGCResult, error) {
	return s.PushImageGCActor("controller", req)
}

func (s *Store) PushImageGCActor(actor string, req ImageGCRequest) (ImageGCResult, error) {
	namespace := normalizeNamespace(req.Namespace)
	labels := cloneLabels(req.RequiredLabels)
	capabilities := sortedUniqueStrings(req.RequiredCapabilities)
	olderThan, err := normalizeDurationString(req.OlderThan, "image gc older_than")
	if err != nil {
		return ImageGCResult{}, err
	}
	now := s.now().UTC()
	actor = normalizeActor(actor)

	s.mu.Lock()
	defer s.mu.Unlock()
	if !req.DryRun {
		s.reconcileLocked(now)
	}
	result := ImageGCResult{
		ID:                   s.nextImageGCIDLocked(now),
		Created:              now,
		Namespace:            namespace,
		RequiredLabels:       labels,
		RequiredCapabilities: capabilities,
		OlderThan:            olderThan,
		Apply:                req.Apply,
		DryRun:               req.DryRun,
	}
	for _, host := range s.sortedHostsLocked() {
		host = s.statusLocked(host)
		if missing := missingLabels(host.Labels, labels); len(missing) > 0 {
			result.Skipped = append(result.Skipped, ImageGCSkip{
				WorkerID:      host.ID,
				Reason:        "label",
				MissingLabels: missing,
			})
			continue
		}
		if missing := missingCapabilities(host.Capabilities, capabilities); len(missing) > 0 {
			result.Skipped = append(result.Skipped, ImageGCSkip{
				WorkerID:            host.ID,
				Reason:              "capability",
				MissingCapabilities: missing,
			})
			continue
		}
		if host.Status != "ready" {
			result.Skipped = append(result.Skipped, ImageGCSkip{WorkerID: host.ID, Reason: "status", Status: host.Status})
			continue
		}
		if s.activeImageGCLocked(host.ID) {
			result.Skipped = append(result.Skipped, ImageGCSkip{WorkerID: host.ID, Reason: "active"})
			continue
		}
		id := s.nextAssignmentIDLocked(now)
		if req.DryRun {
			id = fmt.Sprintf("planned-%s-%d", id, len(result.Assignments)+1)
		}
		assignment := Assignment{
			ID:                   id,
			Namespace:            namespace,
			WorkerID:             host.ID,
			RequiredLabels:       labels,
			RequiredCapabilities: capabilities,
			Verb:                 "cove",
			Args:                 imageGCArgs(olderThan, req.Apply),
			Status:               "pending",
			Created:              now,
			Updated:              now,
		}
		if !req.DryRun {
			s.assignments[assignment.ID] = assignment
		}
		result.Assignments = append(result.Assignments, cloneAssignment(assignment))
	}
	if len(result.Assignments) == 0 && len(result.Skipped) == 0 {
		return result, fmt.Errorf("no workers match image gc")
	}
	result = normalizeImageGCResult(result)
	if req.DryRun {
		return result, nil
	}
	s.imageGCRuns = append(s.imageGCRuns, cloneImageGCResult(result))
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
	return result, nil
}

func (s *Store) GetImageGCRun(id string) (ImageGCResult, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ImageGCResult{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, run := range s.imageGCRuns {
		if run.ID == id {
			return cloneImageGCResult(run), true
		}
	}
	return ImageGCResult{}, false
}

func (s *Store) ListImageGCRunsPage(filter ImageGCListFilter) ImageGCListResult {
	filter.Namespace = normalizeNamespace(filter.Namespace)
	filter.OlderThan = strings.TrimSpace(filter.OlderThan)
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	if filter.Limit < 0 {
		filter.Limit = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runs := s.sortedImageGCRunsLocked()
	filtered := runs[:0]
	for _, run := range runs {
		if !namespaceMatches(run.Namespace, filter.Namespace) {
			continue
		}
		if filter.OlderThan != "" && run.OlderThan != filter.OlderThan {
			continue
		}
		if filter.Apply != nil && run.Apply != *filter.Apply {
			continue
		}
		filtered = append(filtered, run)
	}
	result := ImageGCListResult{Offset: filter.Offset, Limit: filter.Limit}
	if filter.Offset >= len(filtered) {
		return result
	}
	end := len(filtered) - filter.Offset
	start := 0
	if filter.Limit > 0 && end > filter.Limit {
		start = end - filter.Limit
		result.NextOffset = filter.Offset + filter.Limit
	}
	result.Runs = cloneImageGCResults(filtered[start:end])
	result.Count = len(result.Runs)
	return result
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
	capabilities := sortedUniqueStrings(req.RequiredCapabilities)
	idleTimeout := strings.TrimSpace(req.IdleTimeout)
	maxAge := strings.TrimSpace(req.MaxAge)
	now := s.now().UTC()
	actor = normalizeActor(actor)

	s.mu.Lock()
	defer s.mu.Unlock()
	if !req.DryRun {
		s.reconcileLocked(now)
	}
	result := LifecyclePolicyResult{
		ID:                   s.nextLifecyclePolicyIDLocked(now),
		Created:              now,
		Namespace:            namespace,
		VMName:               vmName,
		RequiredLabels:       labels,
		RequiredCapabilities: capabilities,
		Clear:                req.Clear,
		IdleTimeout:          idleTimeout,
		MaxAge:               maxAge,
		RunBudget:            req.RunBudget,
		DryRun:               req.DryRun,
	}
	for _, host := range s.sortedHostsLocked() {
		host = s.statusLocked(host)
		if missing := missingLabels(host.Labels, labels); len(missing) > 0 {
			result.Skipped = append(result.Skipped, LifecyclePolicySkip{
				WorkerID:      host.ID,
				Reason:        "label",
				MissingLabels: missing,
			})
			continue
		}
		if missing := missingCapabilities(host.Capabilities, capabilities); len(missing) > 0 {
			result.Skipped = append(result.Skipped, LifecyclePolicySkip{
				WorkerID:            host.ID,
				Reason:              "capability",
				MissingCapabilities: missing,
			})
			continue
		}
		if host.Status != "ready" {
			result.Skipped = append(result.Skipped, LifecyclePolicySkip{WorkerID: host.ID, Reason: "status", Status: host.Status})
			continue
		}
		if s.activeLifecyclePolicyLocked(host.ID, vmName) {
			result.Skipped = append(result.Skipped, LifecyclePolicySkip{WorkerID: host.ID, Reason: "active"})
			continue
		}
		id := s.nextAssignmentIDLocked(now)
		if req.DryRun {
			id = fmt.Sprintf("planned-%s-%d", id, len(result.Assignments)+1)
		}
		assignment := Assignment{
			ID:                   id,
			Namespace:            namespace,
			WorkerID:             host.ID,
			RequiredLabels:       labels,
			RequiredCapabilities: capabilities,
			Verb:                 "cove",
			Args:                 cloneStrings(args),
			Status:               "pending",
			Created:              now,
			Updated:              now,
		}
		if !req.DryRun {
			s.assignments[assignment.ID] = assignment
		}
		result.Assignments = append(result.Assignments, cloneAssignment(assignment))
	}
	if len(result.Assignments) == 0 && len(result.Skipped) == 0 {
		return result, fmt.Errorf("no workers match lifecycle policy")
	}
	result = normalizeLifecyclePolicyResult(result)
	if req.DryRun {
		return result, nil
	}
	s.lifecycleRuns = append(s.lifecycleRuns, cloneLifecyclePolicyResult(result))
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
	return result, nil
}

func (s *Store) GetLifecyclePolicyRun(id string) (LifecyclePolicyResult, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return LifecyclePolicyResult{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, run := range s.lifecycleRuns {
		if run.ID == id {
			return cloneLifecyclePolicyResult(run), true
		}
	}
	return LifecyclePolicyResult{}, false
}

func (s *Store) ListLifecyclePolicyRunsPage(filter LifecyclePolicyListFilter) LifecyclePolicyListResult {
	filter.Namespace = normalizeNamespace(filter.Namespace)
	filter.VMName = strings.TrimSpace(filter.VMName)
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	if filter.Limit < 0 {
		filter.Limit = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runs := s.sortedLifecyclePolicyRunsLocked()
	filtered := runs[:0]
	for _, run := range runs {
		if !namespaceMatches(run.Namespace, filter.Namespace) {
			continue
		}
		if filter.VMName != "" && run.VMName != filter.VMName {
			continue
		}
		if filter.Clear != nil && run.Clear != *filter.Clear {
			continue
		}
		filtered = append(filtered, run)
	}
	result := LifecyclePolicyListResult{Offset: filter.Offset, Limit: filter.Limit}
	if filter.Offset >= len(filtered) {
		return result
	}
	end := len(filtered) - filter.Offset
	start := 0
	if filter.Limit > 0 && end > filter.Limit {
		start = end - filter.Limit
		result.NextOffset = filter.Offset + filter.Limit
	}
	result.Runs = cloneLifecyclePolicyResults(filtered[start:end])
	result.Count = len(result.Runs)
	return result
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
	capabilities := sortedUniqueStrings(req.RequiredCapabilities)
	target := strings.TrimSpace(req.Target)
	warnPct := cloneIntPtr(req.WarnPct)
	hardPct := cloneIntPtr(req.HardPct)
	now := s.now().UTC()
	actor = normalizeActor(actor)

	s.mu.Lock()
	defer s.mu.Unlock()
	if !req.DryRun {
		s.reconcileLocked(now)
	}
	result := StorageBudgetResult{
		ID:                   s.nextStorageBudgetIDLocked(now),
		Created:              now,
		Namespace:            namespace,
		RequiredLabels:       labels,
		RequiredCapabilities: capabilities,
		Clear:                req.Clear,
		Target:               target,
		WarnPct:              warnPct,
		HardPct:              hardPct,
		DryRun:               req.DryRun,
	}
	for _, host := range s.sortedHostsLocked() {
		host = s.statusLocked(host)
		if missing := missingLabels(host.Labels, labels); len(missing) > 0 {
			result.Skipped = append(result.Skipped, StoragePolicySkip{
				WorkerID:      host.ID,
				Reason:        "label",
				MissingLabels: missing,
			})
			continue
		}
		if missing := missingCapabilities(host.Capabilities, capabilities); len(missing) > 0 {
			result.Skipped = append(result.Skipped, StoragePolicySkip{
				WorkerID:            host.ID,
				Reason:              "capability",
				MissingCapabilities: missing,
			})
			continue
		}
		if host.Status != "ready" {
			result.Skipped = append(result.Skipped, StoragePolicySkip{WorkerID: host.ID, Reason: "status", Status: host.Status})
			continue
		}
		if s.activeStorageBudgetLocked(host.ID) {
			result.Skipped = append(result.Skipped, StoragePolicySkip{WorkerID: host.ID, Reason: "active"})
			continue
		}
		id := s.nextAssignmentIDLocked(now)
		if req.DryRun {
			id = fmt.Sprintf("planned-%s-%d", id, len(result.Assignments)+1)
		}
		assignment := Assignment{
			ID:                   id,
			Namespace:            namespace,
			WorkerID:             host.ID,
			RequiredLabels:       labels,
			RequiredCapabilities: capabilities,
			Verb:                 "cove",
			Args:                 cloneStrings(args),
			Status:               "pending",
			Created:              now,
			Updated:              now,
		}
		if !req.DryRun {
			s.assignments[assignment.ID] = assignment
		}
		result.Assignments = append(result.Assignments, cloneAssignment(assignment))
	}
	if len(result.Assignments) == 0 && len(result.Skipped) == 0 {
		return result, fmt.Errorf("no workers match storage budget")
	}
	result = normalizeStorageBudgetResult(result)
	if req.DryRun {
		return result, nil
	}
	s.storageBudgetRuns = append(s.storageBudgetRuns, cloneStorageBudgetResult(result))
	if len(result.Assignments) > 0 {
		s.appendAuditLocked(now, AuditEvent{
			Actor:      actor,
			Namespace:  namespace,
			Action:     "storage.budget",
			TargetType: "storage",
			Fields: map[string]string{
				"assignments": strconv.Itoa(len(result.Assignments)),
				"clear":       strconv.FormatBool(req.Clear),
				"target":      target,
			},
		})
	}
	if err := s.persistLocked(); err != nil {
		return result, err
	}
	return result, nil
}

func (s *Store) GetStorageBudgetRun(id string) (StorageBudgetResult, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return StorageBudgetResult{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, run := range s.storageBudgetRuns {
		if run.ID == id {
			return cloneStorageBudgetResult(run), true
		}
	}
	return StorageBudgetResult{}, false
}

func (s *Store) ListStorageBudgetRunsPage(filter StorageBudgetListFilter) StorageBudgetListResult {
	filter.Namespace = normalizeNamespace(filter.Namespace)
	filter.Target = strings.TrimSpace(filter.Target)
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	if filter.Limit < 0 {
		filter.Limit = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runs := s.sortedStorageBudgetRunsLocked()
	filtered := runs[:0]
	for _, run := range runs {
		if !namespaceMatches(run.Namespace, filter.Namespace) {
			continue
		}
		if filter.Target != "" && run.Target != filter.Target {
			continue
		}
		if filter.Clear != nil && run.Clear != *filter.Clear {
			continue
		}
		filtered = append(filtered, run)
	}
	result := StorageBudgetListResult{Offset: filter.Offset, Limit: filter.Limit}
	if filter.Offset >= len(filtered) {
		return result
	}
	end := len(filtered) - filter.Offset
	start := 0
	if filter.Limit > 0 && end > filter.Limit {
		start = end - filter.Limit
		result.NextOffset = filter.Offset + filter.Limit
	}
	result.Runs = cloneStorageBudgetResults(filtered[start:end])
	result.Count = len(result.Runs)
	return result
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
	capabilities := sortedUniqueStrings(req.RequiredCapabilities)
	category := strings.TrimSpace(req.Category)
	olderThan := strings.TrimSpace(req.OlderThan)
	now := s.now().UTC()
	actor = normalizeActor(actor)

	s.mu.Lock()
	defer s.mu.Unlock()
	if !req.DryRun {
		s.reconcileLocked(now)
	}
	result := StoragePruneResult{
		ID:                   s.nextStoragePruneIDLocked(now),
		Created:              now,
		Namespace:            namespace,
		RequiredLabels:       labels,
		RequiredCapabilities: capabilities,
		Category:             category,
		OlderThan:            olderThan,
		Apply:                req.Apply,
		DryRun:               req.DryRun,
	}
	for _, host := range s.sortedHostsLocked() {
		host = s.statusLocked(host)
		if missing := missingLabels(host.Labels, labels); len(missing) > 0 {
			result.Skipped = append(result.Skipped, StoragePolicySkip{
				WorkerID:      host.ID,
				Reason:        "label",
				MissingLabels: missing,
			})
			continue
		}
		if missing := missingCapabilities(host.Capabilities, capabilities); len(missing) > 0 {
			result.Skipped = append(result.Skipped, StoragePolicySkip{
				WorkerID:            host.ID,
				Reason:              "capability",
				MissingCapabilities: missing,
			})
			continue
		}
		if host.Status != "ready" {
			result.Skipped = append(result.Skipped, StoragePolicySkip{WorkerID: host.ID, Reason: "status", Status: host.Status})
			continue
		}
		if s.activeStoragePruneLocked(host.ID) {
			result.Skipped = append(result.Skipped, StoragePolicySkip{WorkerID: host.ID, Reason: "active"})
			continue
		}
		id := s.nextAssignmentIDLocked(now)
		if req.DryRun {
			id = fmt.Sprintf("planned-%s-%d", id, len(result.Assignments)+1)
		}
		assignment := Assignment{
			ID:                   id,
			Namespace:            namespace,
			WorkerID:             host.ID,
			RequiredLabels:       labels,
			RequiredCapabilities: capabilities,
			Verb:                 "cove",
			Args:                 cloneStrings(args),
			Status:               "pending",
			Created:              now,
			Updated:              now,
		}
		if !req.DryRun {
			s.assignments[assignment.ID] = assignment
		}
		result.Assignments = append(result.Assignments, cloneAssignment(assignment))
	}
	if len(result.Assignments) == 0 && len(result.Skipped) == 0 {
		return result, fmt.Errorf("no workers match storage prune")
	}
	result = normalizeStoragePruneResult(result)
	if req.DryRun {
		return result, nil
	}
	s.storagePruneRuns = append(s.storagePruneRuns, cloneStoragePruneResult(result))
	if len(result.Assignments) > 0 {
		s.appendAuditLocked(now, AuditEvent{
			Actor:      actor,
			Namespace:  namespace,
			Action:     "storage.prune",
			TargetType: "storage",
			Fields: map[string]string{
				"assignments": strconv.Itoa(len(result.Assignments)),
				"apply":       strconv.FormatBool(req.Apply),
				"category":    category,
			},
		})
	}
	if err := s.persistLocked(); err != nil {
		return result, err
	}
	return result, nil
}

func (s *Store) GetStoragePruneRun(id string) (StoragePruneResult, bool) {
	id = strings.TrimSpace(id)
	if id == "" {
		return StoragePruneResult{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, run := range s.storagePruneRuns {
		if run.ID == id {
			return cloneStoragePruneResult(run), true
		}
	}
	return StoragePruneResult{}, false
}

func (s *Store) ListStoragePruneRunsPage(filter StoragePruneListFilter) StoragePruneListResult {
	filter.Namespace = normalizeNamespace(filter.Namespace)
	filter.Category = strings.TrimSpace(filter.Category)
	filter.OlderThan = strings.TrimSpace(filter.OlderThan)
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	if filter.Limit < 0 {
		filter.Limit = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runs := s.sortedStoragePruneRunsLocked()
	filtered := runs[:0]
	for _, run := range runs {
		if !namespaceMatches(run.Namespace, filter.Namespace) {
			continue
		}
		if filter.Category != "" && run.Category != filter.Category {
			continue
		}
		if filter.OlderThan != "" && run.OlderThan != filter.OlderThan {
			continue
		}
		if filter.Apply != nil && run.Apply != *filter.Apply {
			continue
		}
		filtered = append(filtered, run)
	}
	result := StoragePruneListResult{Offset: filter.Offset, Limit: filter.Limit}
	if filter.Offset >= len(filtered) {
		return result
	}
	end := len(filtered) - filter.Offset
	start := 0
	if filter.Limit > 0 && end > filter.Limit {
		start = end - filter.Limit
		result.NextOffset = filter.Offset + filter.Limit
	}
	result.Runs = cloneStoragePruneResults(filtered[start:end])
	result.Count = len(result.Runs)
	return result
}

func (s *Store) ListControllerRunsPage(filter ControllerRunListFilter) ControllerRunListResult {
	filter.Namespace = normalizeNamespace(filter.Namespace)
	filter.Kind = strings.TrimSpace(filter.Kind)
	filter.TargetType = strings.TrimSpace(filter.TargetType)
	filter.TargetID = strings.TrimSpace(filter.TargetID)
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	if filter.Limit < 0 {
		filter.Limit = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	runs := s.sortedControllerRunsLocked()
	filtered := runs[:0]
	for _, run := range runs {
		if !namespaceMatches(run.Namespace, filter.Namespace) {
			continue
		}
		if filter.Kind != "" && run.Kind != filter.Kind {
			continue
		}
		if filter.TargetType != "" && run.TargetType != filter.TargetType {
			continue
		}
		if filter.TargetID != "" && run.TargetID != filter.TargetID {
			continue
		}
		filtered = append(filtered, run)
	}
	result := ControllerRunListResult{Offset: filter.Offset, Limit: filter.Limit}
	if filter.Offset >= len(filtered) {
		return result
	}
	end := len(filtered) - filter.Offset
	start := 0
	if filter.Limit > 0 && end > filter.Limit {
		start = end - filter.Limit
		result.NextOffset = filter.Offset + filter.Limit
	}
	result.Runs = cloneControllerRunSummaries(filtered[start:end])
	result.Count = len(result.Runs)
	return result
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
	if worker.Quarantined {
		if reconciled.changed() {
			if err := s.persistLocked(); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}
	assignments := s.sortedLeaseAssignmentsLocked()
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
		if !assignment.RetryAt.IsZero() && now.Before(assignment.RetryAt) {
			continue
		}
		assignment.Status = "leased"
		assignment.LeasedTo = id
		assignment.LeaseExpires = now.Add(s.assignmentTTL)
		assignment.QueueTTL = ""
		assignment.QueueExpires = time.Time{}
		assignment.RetryAt = time.Time{}
		assignment.Attempt++
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
	return s.ListAssignmentsFiltered(AssignmentListFilter{Namespace: namespace})
}

func (s *Store) ListAssignmentsFiltered(filter AssignmentListFilter) []Assignment {
	return s.ListAssignmentsPage(filter).Assignments
}

func (s *Store) ListAssignmentsPage(filter AssignmentListFilter) AssignmentListResult {
	filter.Namespace = normalizeNamespace(filter.Namespace)
	filter.Status = strings.TrimSpace(filter.Status)
	filter.WorkerID = strings.TrimSpace(filter.WorkerID)
	filter.LeasedTo = strings.TrimSpace(filter.LeasedTo)
	filter.Verb = strings.TrimSpace(filter.Verb)
	filter.ImageRef = strings.TrimSpace(filter.ImageRef)
	filter.SandboxID = strings.TrimSpace(filter.SandboxID)
	filter.WarmPool = strings.TrimSpace(filter.WarmPool)
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	if filter.Limit < 0 {
		filter.Limit = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	assignments := s.sortedAssignmentsLocked()
	result := AssignmentListResult{Offset: filter.Offset, Limit: filter.Limit}
	offset := 0
	for _, assignment := range assignments {
		if !namespaceMatches(assignment.Namespace, filter.Namespace) {
			continue
		}
		if filter.Status != "" && normalizeOperationStatus(assignment.Status) != filter.Status {
			continue
		}
		if filter.WorkerID != "" && assignment.WorkerID != filter.WorkerID {
			continue
		}
		if filter.LeasedTo != "" && assignment.LeasedTo != filter.LeasedTo {
			continue
		}
		if filter.Verb != "" && assignment.Verb != filter.Verb {
			continue
		}
		if filter.ImageRef != "" && assignment.ImageRef != filter.ImageRef {
			continue
		}
		if filter.SandboxID != "" && assignment.SandboxID != filter.SandboxID {
			continue
		}
		if filter.WarmPool != "" && assignment.WarmPool != filter.WarmPool {
			continue
		}
		if offset < filter.Offset {
			offset++
			continue
		}
		if filter.Limit > 0 && len(result.Assignments) >= filter.Limit {
			result.NextOffset = filter.Offset + len(result.Assignments)
			break
		}
		result.Assignments = append(result.Assignments, cloneAssignment(assignment))
	}
	result.Count = len(result.Assignments)
	return result
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
	return s.ListAuditPage(AuditListFilter{Namespace: namespace, Limit: limit}).Events
}

func (s *Store) ListAuditFiltered(filter AuditListFilter) []AuditEvent {
	return s.ListAuditPage(filter).Events
}

func (s *Store) ListAuditPage(filter AuditListFilter) AuditListResult {
	filter.Namespace = normalizeNamespace(filter.Namespace)
	filter.Actor = strings.TrimSpace(filter.Actor)
	filter.Action = strings.TrimSpace(filter.Action)
	filter.TargetType = strings.TrimSpace(filter.TargetType)
	filter.TargetID = strings.TrimSpace(filter.TargetID)
	filter.WorkerID = strings.TrimSpace(filter.WorkerID)
	filter.AssignmentID = strings.TrimSpace(filter.AssignmentID)
	filter.SandboxID = strings.TrimSpace(filter.SandboxID)
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	if filter.Limit < 0 {
		filter.Limit = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	events := s.sortedAuditLocked()
	filtered := events[:0]
	for _, event := range events {
		if !namespaceMatches(event.Namespace, filter.Namespace) {
			continue
		}
		if filter.Actor != "" && event.Actor != filter.Actor {
			continue
		}
		if filter.Action != "" && event.Action != filter.Action {
			continue
		}
		if filter.TargetType != "" && event.TargetType != filter.TargetType {
			continue
		}
		if filter.TargetID != "" && event.TargetID != filter.TargetID {
			continue
		}
		if filter.WorkerID != "" && event.WorkerID != filter.WorkerID {
			continue
		}
		if filter.AssignmentID != "" && event.AssignmentID != filter.AssignmentID {
			continue
		}
		if filter.SandboxID != "" && !auditEventMatchesSandbox(event, filter.SandboxID) {
			continue
		}
		filtered = append(filtered, event)
	}
	result := AuditListResult{Offset: filter.Offset, Limit: filter.Limit}
	if filter.Offset >= len(filtered) {
		return result
	}
	end := len(filtered) - filter.Offset
	start := 0
	if filter.Limit > 0 && end > filter.Limit {
		start = end - filter.Limit
		result.NextOffset = filter.Offset + filter.Limit
	}
	result.Events = cloneAuditEvents(filtered[start:end])
	result.Count = len(result.Events)
	return result
}

func (s *Store) ListSandboxEventsPage(namespace, id string, filter AuditListFilter) AuditListResult {
	filter.Namespace = namespace
	filter.SandboxID = strings.TrimSpace(id)
	return s.ListAuditPage(filter)
}

func (s *Store) ListAssignmentEventsPage(namespace, id string, filter AuditListFilter) AuditListResult {
	filter.Namespace = namespace
	filter.AssignmentID = strings.TrimSpace(id)
	return s.ListAuditPage(filter)
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
	if principal, ok := s.authenticateSAMLSession(token); ok {
		return principal, true
	}
	if principal, ok := s.authenticateOIDCBearer(token); ok {
		return principal, true
	}
	return s.authenticateSAMLBearer(token)
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

func (s *Store) UpsertSAMLBinding(req SAMLBindingRequest) (SAMLBindingResult, error) {
	return s.UpsertSAMLBindingActor("controller", req)
}

func (s *Store) UpsertSAMLBindingActor(actor string, req SAMLBindingRequest) (SAMLBindingResult, error) {
	now := s.now().UTC()
	record := samlBindingRecord{
		Name:           strings.TrimSpace(req.Name),
		EntityID:       strings.TrimSpace(req.EntityID),
		Subject:        strings.TrimSpace(req.Subject),
		SSOURL:         strings.TrimSpace(req.SSOURL),
		Audience:       strings.TrimSpace(req.Audience),
		Namespace:      normalizeNamespace(req.Namespace),
		Role:           strings.TrimSpace(req.Role),
		CertificatePEM: strings.TrimSpace(req.CertificatePEM),
		MetadataURL:    strings.TrimSpace(req.MetadataURL),
	}
	if err := applySAMLBindingMetadataRequest(&record, req, now); err != nil {
		return SAMLBindingResult{}, err
	}
	var err error
	record, err = normalizeSAMLBindingRecord(record)
	if err != nil {
		return SAMLBindingResult{}, err
	}
	actor = normalizeActor(actor)
	record.Created = now
	record.Updated = now

	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.samlBindings[record.Name]; ok && !old.Created.IsZero() {
		if old.Namespace != record.Namespace {
			return SAMLBindingResult{}, fmt.Errorf("saml binding %q already exists in another namespace", record.Name)
		}
		record.Created = old.Created
	}
	s.samlBindings[record.Name] = record
	s.appendAuditLocked(now, AuditEvent{
		Actor:      actor,
		Namespace:  record.Namespace,
		Action:     "saml_binding.upsert",
		TargetType: "saml_binding",
		TargetID:   record.Name,
		Fields: map[string]string{
			"entity_id":          record.EntityID,
			"subject":            record.Subject,
			"audience":           record.Audience,
			"role":               record.Role,
			"sso_url":            record.SSOURL,
			"metadata_url":       record.MetadataURL,
			"certificate_sha256": samlCertificateSHA256(record.CertificatePEM),
		},
	})
	if err := s.persistLocked(); err != nil {
		return SAMLBindingResult{}, err
	}
	return SAMLBindingResult{Binding: publicSAMLBinding(record)}, nil
}

func (s *Store) DeleteSAMLBinding(name string) (SAMLBindingResult, error) {
	return s.DeleteSAMLBindingActor("controller", name)
}

func (s *Store) RefreshSAMLBindingMetadata(name string) (SAMLBindingResult, error) {
	return s.RefreshSAMLBindingMetadataActor("controller", name)
}

func (s *Store) RefreshSAMLBindingMetadataActor(actor, name string) (SAMLBindingResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return SAMLBindingResult{}, fmt.Errorf("saml binding name required")
	}
	now := s.now().UTC()
	actor = normalizeActor(actor)

	s.mu.Lock()
	record, ok := s.samlBindings[name]
	s.mu.Unlock()
	if !ok {
		return SAMLBindingResult{}, fmt.Errorf("saml binding %q not found", name)
	}
	if strings.TrimSpace(record.MetadataURL) == "" {
		return SAMLBindingResult{}, fmt.Errorf("saml binding %q metadata_url required", name)
	}
	if err := applySAMLBindingMetadataURL(&record, now); err != nil {
		return SAMLBindingResult{}, err
	}
	var err error
	record, err = normalizeSAMLBindingRecord(record)
	if err != nil {
		return SAMLBindingResult{}, err
	}
	record.Updated = now

	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.samlBindings[name]
	if !ok {
		return SAMLBindingResult{}, fmt.Errorf("saml binding %q not found", name)
	}
	if current.Namespace != record.Namespace {
		return SAMLBindingResult{}, fmt.Errorf("saml binding %q changed namespace during refresh", name)
	}
	record.Created = current.Created
	s.samlBindings[name] = record
	s.appendAuditLocked(now, AuditEvent{
		Actor:      actor,
		Namespace:  record.Namespace,
		Action:     "saml_binding.refresh",
		TargetType: "saml_binding",
		TargetID:   record.Name,
		Fields: map[string]string{
			"entity_id":          record.EntityID,
			"sso_url":            record.SSOURL,
			"metadata_url":       record.MetadataURL,
			"certificate_sha256": samlCertificateSHA256(record.CertificatePEM),
		},
	})
	if err := s.persistLocked(); err != nil {
		return SAMLBindingResult{}, err
	}
	return SAMLBindingResult{Binding: publicSAMLBinding(record)}, nil
}

func (s *Store) DeleteSAMLBindingActor(actor, name string) (SAMLBindingResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return SAMLBindingResult{}, fmt.Errorf("saml binding name required")
	}
	now := s.now().UTC()
	actor = normalizeActor(actor)

	s.mu.Lock()
	defer s.mu.Unlock()
	record, ok := s.samlBindings[name]
	if !ok {
		return SAMLBindingResult{}, fmt.Errorf("saml binding %q not found", name)
	}
	delete(s.samlBindings, name)
	s.appendAuditLocked(now, AuditEvent{
		Actor:      actor,
		Namespace:  record.Namespace,
		Action:     "saml_binding.delete",
		TargetType: "saml_binding",
		TargetID:   name,
	})
	if err := s.persistLocked(); err != nil {
		return SAMLBindingResult{}, err
	}
	return SAMLBindingResult{Binding: publicSAMLBinding(record)}, nil
}

func (s *Store) ListSAMLBindings() []SAMLBinding {
	return s.ListSAMLBindingsNamespace("")
}

func (s *Store) ListSAMLBindingsNamespace(namespace string) []SAMLBinding {
	namespace = normalizeNamespace(namespace)
	s.mu.Lock()
	defer s.mu.Unlock()
	records := s.sortedSAMLBindingsLocked()
	out := make([]SAMLBinding, 0, len(records))
	for _, record := range records {
		if !namespaceMatches(record.Namespace, namespace) {
			continue
		}
		out = append(out, publicSAMLBinding(record))
	}
	return out
}

func (s *Store) SAMLMetadata(name string) ([]byte, error) {
	return s.SAMLMetadataNamespace(name, "")
}

func (s *Store) SAMLMetadataNamespace(name, namespace string) ([]byte, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("saml binding name required")
	}
	namespace = normalizeNamespace(namespace)
	s.mu.Lock()
	record, ok := s.samlBindings[name]
	if !ok || !namespaceMatches(record.Namespace, namespace) {
		s.mu.Unlock()
		return nil, fmt.Errorf("saml binding %q not found", name)
	}
	binding := publicSAMLBinding(record)
	s.mu.Unlock()
	return samlMetadataXML(binding)
}

func (s *Store) SAMLAuthnRequest(name, relayState string) (SAMLAuthnRequestResult, error) {
	return s.SAMLAuthnRequestNamespace(name, "", relayState)
}

func (s *Store) SAMLAuthnRequestNamespace(name, namespace, relayState string) (SAMLAuthnRequestResult, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return SAMLAuthnRequestResult{}, fmt.Errorf("saml binding name required")
	}
	namespace = normalizeNamespace(namespace)
	now := s.now().UTC()
	s.mu.Lock()
	record, ok := s.samlBindings[name]
	if !ok || !namespaceMatches(record.Namespace, namespace) {
		s.mu.Unlock()
		return SAMLAuthnRequestResult{}, fmt.Errorf("saml binding %q not found", name)
	}
	binding := publicSAMLBinding(record)
	s.mu.Unlock()
	return samlAuthnRequestResult(binding, now, relayState)
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
	return s.ListWorkersPage(WorkerListFilter{}).Workers
}

func (s *Store) ListWorkersFiltered(filter WorkerListFilter) []HostRecord {
	return s.ListWorkersPage(filter).Workers
}

func (s *Store) ListWorkersPage(filter WorkerListFilter) WorkerListResult {
	filter.Status = strings.TrimSpace(filter.Status)
	filter.Host = strings.TrimSpace(filter.Host)
	filter.Version = strings.TrimSpace(filter.Version)
	filter.ImageRef = strings.TrimSpace(filter.ImageRef)
	filter.SourceManifestDigest = strings.TrimSpace(filter.SourceManifestDigest)
	filter.Labels = cloneLabels(filter.Labels)
	filter.Capabilities = sortedUniqueStrings(filter.Capabilities)
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	if filter.Limit < 0 {
		filter.Limit = 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result := WorkerListResult{Offset: filter.Offset, Limit: filter.Limit}
	offset := 0
	for _, host := range s.sortedHostsLocked() {
		host = s.statusLocked(host)
		if filter.Status != "" && host.Status != filter.Status {
			continue
		}
		if filter.Host != "" && host.Host != filter.Host {
			continue
		}
		if filter.Version != "" && host.Version != filter.Version {
			continue
		}
		if !labelsMatch(host.Labels, filter.Labels) {
			continue
		}
		if !capabilitiesMatch(host.Capabilities, filter.Capabilities) {
			continue
		}
		if filter.ImageRef != "" && !workerHasImage(host, filter.ImageRef, filter.SourceManifestDigest) {
			continue
		}
		if offset < filter.Offset {
			offset++
			continue
		}
		if filter.Limit > 0 && len(result.Workers) >= filter.Limit {
			result.NextOffset = filter.Offset + len(result.Workers)
			break
		}
		result.Workers = append(result.Workers, host)
	}
	result.Count = len(result.Workers)
	return result
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
		if assignment.Status == "pending" && assignment.LeasedTo == "" && !assignment.QueueExpires.IsZero() && now.After(assignment.QueueExpires) {
			assignment.Status = "expired"
			assignment.Updated = now
			assignment.LeaseExpires = time.Time{}
			assignment.LastReport = nil
			s.assignments[assignment.ID] = assignment
			result.ExpiredAssignments = append(result.ExpiredAssignments, assignment.ID)
			continue
		}
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
			selected, err := s.selectWorkerLocked(assignmentPolicy(assignment), assignment.ImageRef, assignment.ImageManifestDigest, assignment.RequiredLabels, assignment.RequiredCapabilities, assignment.AntiAffinityKey, assignment.Resources)
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
	sort.Strings(result.ExpiredAssignments)
	sort.Strings(result.WarmPoolAssignments)
	sort.Strings(result.WarmPoolCanceled)
	sort.Strings(result.WarmPoolCleanup)
	return result
}

func (s *Store) statusLocked(record HostRecord) HostRecord {
	record.Status = workerStatus(s.now().UTC(), record)
	record.Labels = cloneLabels(record.Labels)
	record.Capabilities = cloneStrings(record.Capabilities)
	record.ImageRefs = cloneStrings(record.ImageRefs)
	record.ImageDetails = cloneWorkerImages(record.ImageDetails)
	if record.Report != nil {
		report := *record.Report
		record.Report = &report
	}
	return record
}

func workerStatus(now time.Time, record HostRecord) string {
	if now.After(record.Expires) {
		return "stale"
	}
	if record.Quarantined {
		return "quarantined"
	}
	if record.Cordoned {
		return "cordoned"
	}
	switch record.Status {
	case "", "stale", "cordoned", "quarantined":
		return "ready"
	default:
		return record.Status
	}
}

func (r ReconcileResult) changed() bool {
	return len(r.StaleWorkers) > 0 || len(r.RequeuedAssignments) > 0 || len(r.ReplacedAssignments) > 0 || len(r.ExpiredAssignments) > 0 || len(r.WarmPoolAssignments) > 0 || len(r.WarmPoolCanceled) > 0 || len(r.WarmPoolCleanup) > 0
}

func activeAssignmentStatus(status string) bool {
	switch status {
	case "pending", "leased", "running", "ready":
		return true
	default:
		return false
	}
}

func openAssignmentStatus(status string) bool {
	return activeAssignmentStatus(status) || status == "claimed" || sandboxPendingStopStatus(status)
}

func normalizeOperationStatus(status string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return "pending"
	}
	return status
}

func addStatusCount(counts map[string]int, status string) {
	status = normalizeOperationStatus(status)
	counts[status]++
}

func addCapabilityCoverage(coverage map[string]*WorkerCapabilitySummary, host HostRecord) {
	for _, name := range sortedUniqueStrings(host.Capabilities) {
		summary := coverage[name]
		if summary == nil {
			summary = &WorkerCapabilitySummary{
				Name:     name,
				ByStatus: make(map[string]int),
			}
			coverage[name] = summary
		}
		summary.Total++
		addStatusCount(summary.ByStatus, host.Status)
		summary.Workers = append(summary.Workers, host.ID)
		switch host.Status {
		case "ready":
			summary.Ready++
		case "cordoned":
			summary.Cordoned++
		case "quarantined":
			summary.Quarantined++
		case "stale":
			summary.Stale++
		}
	}
}

func sortedCapabilityCoverage(coverage map[string]*WorkerCapabilitySummary) []WorkerCapabilitySummary {
	if len(coverage) == 0 {
		return nil
	}
	names := make([]string, 0, len(coverage))
	for name := range coverage {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]WorkerCapabilitySummary, 0, len(names))
	for _, name := range names {
		summary := *coverage[name]
		sort.Strings(summary.Workers)
		out = append(out, summary)
	}
	return out
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

func canceledAssignmentReportAllowed(assignment Assignment, status, workerID string) bool {
	return normalizeOperationStatus(assignment.Status) == "canceled" &&
		normalizeOperationStatus(status) == "canceled" &&
		strings.TrimSpace(assignment.WorkerID) == strings.TrimSpace(workerID)
}

func decommissionBlocksWorker(status string) bool {
	switch status {
	case "pending", "leased", "running", "ready", "claimed", "draining", "restarting":
		return true
	default:
		return false
	}
}

func decommissionCancelsAssignment(assignment Assignment, workerID string) bool {
	return assignment.WorkerID == workerID && assignment.Status == "pending" && assignment.LeasedTo == ""
}

func decommissionBlockReason(assignment Assignment, workerID string, force bool) string {
	if !force && assignment.Status == "pending" && assignment.LeasedTo == "" {
		return "pending assignment requires force"
	}
	if assignment.LeasedTo == workerID {
		return "assignment leased to worker"
	}
	return "active assignment"
}

func decommissionBlockIDs(blocks []WorkerDecommissionBlock) []string {
	ids := make([]string, 0, len(blocks))
	for _, block := range blocks {
		ids = append(ids, block.AssignmentID)
	}
	return ids
}

func evacuationAssignment(assignment Assignment) WorkerEvacuationAssignment {
	return WorkerEvacuationAssignment{
		AssignmentID: assignment.ID,
		Namespace:    assignment.Namespace,
		SandboxID:    assignment.SandboxID,
		Status:       normalizeOperationStatus(assignment.Status),
		WorkerID:     assignment.WorkerID,
		LeasedTo:     assignment.LeasedTo,
	}
}

func evacuationPendingBlockReason(assignment Assignment) string {
	if !assignmentCanPlace(assignment) {
		return "pinned assignment"
	}
	return "no replacement worker"
}

func evacuationActiveBlockReason(assignment Assignment, workerID string) string {
	if assignment.LeasedTo == workerID {
		return "assignment leased to worker"
	}
	return "active assignment"
}

func assignmentCanPlace(assignment Assignment) bool {
	if assignment.WarmPoolSlot != "" {
		return false
	}
	return assignment.Policy != "" || assignment.ImageRef != "" || len(assignment.RequiredLabels) > 0 || len(assignment.RequiredCapabilities) > 0
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
	plans := s.sortedPlacementPlansLocked()
	preparations := s.sortedImagePreparationsLocked()
	imageGCRuns := s.sortedImageGCRunsLocked()
	lifecycleRuns := s.sortedLifecyclePolicyRunsLocked()
	storageBudgetRuns := s.sortedStorageBudgetRunsLocked()
	storagePruneRuns := s.sortedStoragePruneRunsLocked()
	audit := cloneAuditEvents(s.audit)
	metering := s.sortedMeteringLocked()
	reports := s.sortedAssignmentReportsLocked()
	accounts := s.sortedServiceAccountsLocked()
	oidcBindings := s.sortedOIDCBindingsLocked()
	samlBindings := s.sortedSAMLBindingsLocked()
	samlReplays := s.sortedSAMLReplaysLocked()
	samlSessions := s.sortedSAMLSessionRecordsLocked()
	data, err := json.MarshalIndent(storeFile{Hosts: hosts, Assignments: assignments, WarmPools: warmPools, PlacementPlans: plans, ImagePreparations: preparations, ImageGCRuns: imageGCRuns, LifecycleRuns: lifecycleRuns, StorageBudgetRuns: storageBudgetRuns, StoragePruneRuns: storagePruneRuns, AuditEvents: audit, MeteringRecords: metering, AssignmentReports: reports, ServiceAccounts: accounts, OIDCBindings: oidcBindings, SAMLBindings: samlBindings, SAMLReplays: samlReplays, SAMLSessions: samlSessions}, "", "  ")
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

func (s *Store) sortedLeaseAssignmentsLocked() []Assignment {
	assignments := s.sortedAssignmentsLocked()
	sort.SliceStable(assignments, func(i, j int) bool {
		return assignments[i].Priority > assignments[j].Priority
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

func (s *Store) sortedPlacementPlansLocked() []PlacementPlan {
	plans := clonePlacementPlans(s.plans)
	sort.Slice(plans, func(i, j int) bool {
		if !plans[i].Created.Equal(plans[j].Created) {
			return plans[i].Created.Before(plans[j].Created)
		}
		return plans[i].ID < plans[j].ID
	})
	return plans
}

func (s *Store) sortedImagePreparationsLocked() []ImagePrepareResult {
	preps := cloneImagePrepareResults(s.preparations)
	sort.Slice(preps, func(i, j int) bool {
		if !preps[i].Created.Equal(preps[j].Created) {
			return preps[i].Created.Before(preps[j].Created)
		}
		return preps[i].ID < preps[j].ID
	})
	return preps
}

func (s *Store) sortedImageGCRunsLocked() []ImageGCResult {
	runs := cloneImageGCResults(s.imageGCRuns)
	sort.Slice(runs, func(i, j int) bool {
		if !runs[i].Created.Equal(runs[j].Created) {
			return runs[i].Created.Before(runs[j].Created)
		}
		return runs[i].ID < runs[j].ID
	})
	return runs
}

func (s *Store) sortedLifecyclePolicyRunsLocked() []LifecyclePolicyResult {
	runs := cloneLifecyclePolicyResults(s.lifecycleRuns)
	sort.Slice(runs, func(i, j int) bool {
		if !runs[i].Created.Equal(runs[j].Created) {
			return runs[i].Created.Before(runs[j].Created)
		}
		return runs[i].ID < runs[j].ID
	})
	return runs
}

func (s *Store) sortedStorageBudgetRunsLocked() []StorageBudgetResult {
	runs := cloneStorageBudgetResults(s.storageBudgetRuns)
	sort.Slice(runs, func(i, j int) bool {
		if !runs[i].Created.Equal(runs[j].Created) {
			return runs[i].Created.Before(runs[j].Created)
		}
		return runs[i].ID < runs[j].ID
	})
	return runs
}

func (s *Store) sortedStoragePruneRunsLocked() []StoragePruneResult {
	runs := cloneStoragePruneResults(s.storagePruneRuns)
	sort.Slice(runs, func(i, j int) bool {
		if !runs[i].Created.Equal(runs[j].Created) {
			return runs[i].Created.Before(runs[j].Created)
		}
		return runs[i].ID < runs[j].ID
	})
	return runs
}

func (s *Store) sortedControllerRunsLocked() []ControllerRunSummary {
	var runs []ControllerRunSummary
	for _, plan := range s.sortedPlacementPlansLocked() {
		runs = append(runs, controllerRunFromPlacementPlan(plan))
	}
	for _, prep := range s.sortedImagePreparationsLocked() {
		runs = append(runs, controllerRunFromImagePrepare(prep))
	}
	for _, run := range s.sortedImageGCRunsLocked() {
		runs = append(runs, controllerRunFromImageGC(run))
	}
	for _, run := range s.sortedLifecyclePolicyRunsLocked() {
		runs = append(runs, controllerRunFromLifecyclePolicy(run))
	}
	for _, run := range s.sortedStorageBudgetRunsLocked() {
		runs = append(runs, controllerRunFromStorageBudget(run))
	}
	for _, run := range s.sortedStoragePruneRunsLocked() {
		runs = append(runs, controllerRunFromStoragePrune(run))
	}
	sort.Slice(runs, func(i, j int) bool {
		if !runs[i].Created.Equal(runs[j].Created) {
			return runs[i].Created.Before(runs[j].Created)
		}
		if runs[i].Kind != runs[j].Kind {
			return runs[i].Kind < runs[j].Kind
		}
		return runs[i].ID < runs[j].ID
	})
	return runs
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

func (s *Store) sortedAssignmentReportsLocked() []AssignmentReport {
	reports := cloneAssignmentReports(s.reports)
	sort.Slice(reports, func(i, j int) bool {
		if !reports[i].Report.Time.Equal(reports[j].Report.Time) {
			return reports[i].Report.Time.Before(reports[j].Report.Time)
		}
		if reports[i].AssignmentID != reports[j].AssignmentID {
			return reports[i].AssignmentID < reports[j].AssignmentID
		}
		return reports[i].WorkerID < reports[j].WorkerID
	})
	return reports
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

func (s *Store) sortedSAMLBindingsLocked() []samlBindingRecord {
	records := make([]samlBindingRecord, 0, len(s.samlBindings))
	for _, record := range s.samlBindings {
		records = append(records, cloneSAMLBindingRecord(record))
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].Name < records[j].Name
	})
	return records
}

func (s *Store) sortedSAMLReplaysLocked() []samlReplayRecord {
	records := make([]samlReplayRecord, 0, len(s.samlReplays))
	for _, record := range s.samlReplays {
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].Binding != records[j].Binding {
			return records[i].Binding < records[j].Binding
		}
		return records[i].AssertionID < records[j].AssertionID
	})
	return records
}

func (s *Store) sortedSAMLSessionRecordsLocked() []samlSessionRecord {
	records := make([]samlSessionRecord, 0, len(s.samlSessions))
	for _, record := range s.samlSessions {
		records = append(records, normalizeSAMLSessionRecord(record))
	}
	sort.Slice(records, func(i, j int) bool {
		if !records[i].Expires.Equal(records[j].Expires) {
			return records[i].Expires.Before(records[j].Expires)
		}
		if records[i].Binding != records[j].Binding {
			return records[i].Binding < records[j].Binding
		}
		return records[i].TokenHash < records[j].TokenHash
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

func (s *Store) nextPlacementPlanIDLocked(now time.Time) string {
	base := fmt.Sprintf("placement-plan-%d", now.UnixNano())
	id := base
	for i := 2; ; i++ {
		found := false
		for _, plan := range s.plans {
			if plan.ID == id {
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

func (s *Store) nextImagePrepareIDLocked(now time.Time) string {
	base := fmt.Sprintf("image-prepare-%d", now.UnixNano())
	id := base
	for i := 2; ; i++ {
		found := false
		for _, prep := range s.preparations {
			if prep.ID == id {
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

func (s *Store) nextImageGCIDLocked(now time.Time) string {
	base := fmt.Sprintf("image-gc-%d", now.UnixNano())
	id := base
	for i := 2; ; i++ {
		found := false
		for _, run := range s.imageGCRuns {
			if run.ID == id {
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

func (s *Store) nextLifecyclePolicyIDLocked(now time.Time) string {
	base := fmt.Sprintf("lifecycle-policy-%d", now.UnixNano())
	id := base
	for i := 2; ; i++ {
		found := false
		for _, run := range s.lifecycleRuns {
			if run.ID == id {
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

func (s *Store) nextStorageBudgetIDLocked(now time.Time) string {
	base := fmt.Sprintf("storage-budget-%d", now.UnixNano())
	id := base
	for i := 2; ; i++ {
		found := false
		for _, run := range s.storageBudgetRuns {
			if run.ID == id {
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

func (s *Store) nextStoragePruneIDLocked(now time.Time) string {
	base := fmt.Sprintf("storage-prune-%d", now.UnixNano())
	id := base
	for i := 2; ; i++ {
		found := false
		for _, run := range s.storagePruneRuns {
			if run.ID == id {
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
	out.RequiredCapabilities = cloneStrings(in.RequiredCapabilities)
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

func clonePlacementPlan(in PlacementPlan) PlacementPlan {
	out := in
	out.RequiredLabels = cloneLabels(in.RequiredLabels)
	out.RequiredCapabilities = cloneStrings(in.RequiredCapabilities)
	out.Candidates = clonePlacementCandidates(in.Candidates)
	out.Skipped = clonePlacementSkips(in.Skipped)
	if !out.Created.IsZero() {
		out.Created = out.Created.UTC()
	}
	return out
}

func clonePlacementPlans(in []PlacementPlan) []PlacementPlan {
	if len(in) == 0 {
		return nil
	}
	out := make([]PlacementPlan, len(in))
	for i := range in {
		out[i] = clonePlacementPlan(in[i])
	}
	return out
}

func cloneImagePrepareResult(in ImagePrepareResult) ImagePrepareResult {
	out := in
	if !out.Created.IsZero() {
		out.Created = out.Created.UTC()
	}
	out.RequiredLabels = cloneLabels(in.RequiredLabels)
	out.RequiredCapabilities = cloneStrings(in.RequiredCapabilities)
	out.Assignments = cloneAssignments(in.Assignments)
	out.Skipped = cloneImagePrepareSkips(in.Skipped)
	return out
}

func cloneImagePrepareResults(in []ImagePrepareResult) []ImagePrepareResult {
	if len(in) == 0 {
		return nil
	}
	out := make([]ImagePrepareResult, len(in))
	for i := range in {
		out[i] = cloneImagePrepareResult(in[i])
	}
	return out
}

func cloneImagePrepareSkips(in []ImagePrepareSkip) []ImagePrepareSkip {
	if len(in) == 0 {
		return nil
	}
	out := make([]ImagePrepareSkip, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].MissingLabels = cloneLabels(in[i].MissingLabels)
		out[i].MissingCapabilities = cloneStrings(in[i].MissingCapabilities)
	}
	return out
}

func cloneImageGCResult(in ImageGCResult) ImageGCResult {
	out := in
	if !out.Created.IsZero() {
		out.Created = out.Created.UTC()
	}
	out.RequiredLabels = cloneLabels(in.RequiredLabels)
	out.RequiredCapabilities = cloneStrings(in.RequiredCapabilities)
	out.Assignments = cloneAssignments(in.Assignments)
	out.Skipped = cloneImageGCSkips(in.Skipped)
	return out
}

func cloneImageGCResults(in []ImageGCResult) []ImageGCResult {
	if len(in) == 0 {
		return nil
	}
	out := make([]ImageGCResult, len(in))
	for i := range in {
		out[i] = cloneImageGCResult(in[i])
	}
	return out
}

func cloneImageGCSkips(in []ImageGCSkip) []ImageGCSkip {
	if len(in) == 0 {
		return nil
	}
	out := make([]ImageGCSkip, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].MissingLabels = cloneLabels(in[i].MissingLabels)
		out[i].MissingCapabilities = cloneStrings(in[i].MissingCapabilities)
	}
	return out
}

func cloneLifecyclePolicyResult(in LifecyclePolicyResult) LifecyclePolicyResult {
	out := in
	if !out.Created.IsZero() {
		out.Created = out.Created.UTC()
	}
	out.RequiredLabels = cloneLabels(in.RequiredLabels)
	out.RequiredCapabilities = cloneStrings(in.RequiredCapabilities)
	out.Assignments = cloneAssignments(in.Assignments)
	out.Skipped = cloneLifecyclePolicySkips(in.Skipped)
	return out
}

func cloneLifecyclePolicyResults(in []LifecyclePolicyResult) []LifecyclePolicyResult {
	if len(in) == 0 {
		return nil
	}
	out := make([]LifecyclePolicyResult, len(in))
	for i := range in {
		out[i] = cloneLifecyclePolicyResult(in[i])
	}
	return out
}

func cloneLifecyclePolicySkips(in []LifecyclePolicySkip) []LifecyclePolicySkip {
	if len(in) == 0 {
		return nil
	}
	out := make([]LifecyclePolicySkip, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].MissingLabels = cloneLabels(in[i].MissingLabels)
		out[i].MissingCapabilities = cloneStrings(in[i].MissingCapabilities)
	}
	return out
}

func cloneStorageBudgetResult(in StorageBudgetResult) StorageBudgetResult {
	out := in
	if !out.Created.IsZero() {
		out.Created = out.Created.UTC()
	}
	out.RequiredLabels = cloneLabels(in.RequiredLabels)
	out.RequiredCapabilities = cloneStrings(in.RequiredCapabilities)
	out.WarnPct = cloneIntPtr(in.WarnPct)
	out.HardPct = cloneIntPtr(in.HardPct)
	out.Assignments = cloneAssignments(in.Assignments)
	out.Skipped = cloneStoragePolicySkips(in.Skipped)
	return out
}

func cloneStorageBudgetResults(in []StorageBudgetResult) []StorageBudgetResult {
	if len(in) == 0 {
		return nil
	}
	out := make([]StorageBudgetResult, len(in))
	for i := range in {
		out[i] = cloneStorageBudgetResult(in[i])
	}
	return out
}

func cloneStoragePruneResult(in StoragePruneResult) StoragePruneResult {
	out := in
	if !out.Created.IsZero() {
		out.Created = out.Created.UTC()
	}
	out.RequiredLabels = cloneLabels(in.RequiredLabels)
	out.RequiredCapabilities = cloneStrings(in.RequiredCapabilities)
	out.Assignments = cloneAssignments(in.Assignments)
	out.Skipped = cloneStoragePolicySkips(in.Skipped)
	return out
}

func cloneStoragePruneResults(in []StoragePruneResult) []StoragePruneResult {
	if len(in) == 0 {
		return nil
	}
	out := make([]StoragePruneResult, len(in))
	for i := range in {
		out[i] = cloneStoragePruneResult(in[i])
	}
	return out
}

func cloneStoragePolicySkips(in []StoragePolicySkip) []StoragePolicySkip {
	if len(in) == 0 {
		return nil
	}
	out := make([]StoragePolicySkip, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].MissingLabels = cloneLabels(in[i].MissingLabels)
		out[i].MissingCapabilities = cloneStrings(in[i].MissingCapabilities)
	}
	return out
}

func cloneIntPtr(in *int) *int {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneControllerRunSummary(in ControllerRunSummary) ControllerRunSummary {
	out := in
	if !out.Created.IsZero() {
		out.Created = out.Created.UTC()
	}
	out.Fields = cloneLabels(in.Fields)
	return out
}

func cloneControllerRunSummaries(in []ControllerRunSummary) []ControllerRunSummary {
	if len(in) == 0 {
		return nil
	}
	out := make([]ControllerRunSummary, len(in))
	for i := range in {
		out[i] = cloneControllerRunSummary(in[i])
	}
	return out
}

func cloneAssignmentMap(in map[string]Assignment) map[string]Assignment {
	out := make(map[string]Assignment, len(in))
	for id, assignment := range in {
		out[id] = cloneAssignment(assignment)
	}
	return out
}

func cloneHostRecord(in HostRecord) HostRecord {
	out := in
	out.Labels = cloneLabels(in.Labels)
	out.Capabilities = cloneStrings(in.Capabilities)
	out.ImageRefs = cloneStrings(in.ImageRefs)
	out.ImageDetails = cloneWorkerImages(in.ImageDetails)
	if in.Report != nil {
		report := *in.Report
		out.Report = &report
	}
	return out
}

func cloneHostMap(in map[string]HostRecord) map[string]HostRecord {
	out := make(map[string]HostRecord, len(in))
	for id, host := range in {
		out[id] = cloneHostRecord(host)
	}
	return out
}

func cloneWarmPool(in WarmPool) WarmPool {
	out := in
	out.RequiredLabels = cloneLabels(in.RequiredLabels)
	out.RequiredCapabilities = cloneStrings(in.RequiredCapabilities)
	out.Args = cloneStrings(in.Args)
	return out
}

func cloneWarmPoolMap(in map[string]WarmPool) map[string]WarmPool {
	out := make(map[string]WarmPool, len(in))
	for name, pool := range in {
		out[name] = cloneWarmPool(pool)
	}
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

func cloneAssignmentReport(in AssignmentReport) AssignmentReport {
	return in
}

func cloneAssignmentReports(in []AssignmentReport) []AssignmentReport {
	if len(in) == 0 {
		return nil
	}
	out := make([]AssignmentReport, len(in))
	for i := range in {
		out[i] = cloneAssignmentReport(in[i])
	}
	return out
}

func sandboxReportFromAssignment(assignment Assignment) SandboxReport {
	report := *assignment.LastReport
	return SandboxReport{
		Namespace:    assignment.Namespace,
		SandboxID:    assignment.SandboxID,
		AssignmentID: assignment.ID,
		Role:         assignment.SandboxRole,
		WorkerID:     assignment.WorkerID,
		Status:       assignment.Status,
		Created:      assignment.Created,
		Updated:      assignment.Updated,
		Report:       report,
	}
}

func assignmentReportFromAssignment(assignment Assignment) AssignmentReport {
	report := *assignment.LastReport
	return AssignmentReport{
		Namespace:    assignment.Namespace,
		AssignmentID: assignment.ID,
		WorkerID:     assignment.WorkerID,
		Status:       assignment.Status,
		Created:      assignment.Created,
		Updated:      assignment.Updated,
		Report:       report,
	}
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

func cloneSAMLBindingRecord(in samlBindingRecord) samlBindingRecord {
	return in
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

func publicSAMLBinding(record samlBindingRecord) SAMLBinding {
	return SAMLBinding{
		Name:              record.Name,
		EntityID:          record.EntityID,
		Subject:           record.Subject,
		SSOURL:            record.SSOURL,
		Audience:          record.Audience,
		Namespace:         record.Namespace,
		Role:              record.Role,
		MetadataURL:       record.MetadataURL,
		MetadataFetched:   record.MetadataFetched,
		CertificateSHA256: samlCertificateSHA256(record.CertificatePEM),
		Created:           record.Created,
		Updated:           record.Updated,
	}
}

type samlMetadataEntityDescriptor struct {
	XMLName         xml.Name                    `xml:"md:EntityDescriptor"`
	XMLNSMD         string                      `xml:"xmlns:md,attr"`
	EntityID        string                      `xml:"entityID,attr"`
	SPSSODescriptor samlMetadataSPSSODescriptor `xml:"md:SPSSODescriptor"`
}

type samlMetadataSPSSODescriptor struct {
	AuthnRequestsSigned        string                        `xml:"AuthnRequestsSigned,attr"`
	WantAssertionsSigned       string                        `xml:"WantAssertionsSigned,attr"`
	ProtocolSupportEnumeration string                        `xml:"protocolSupportEnumeration,attr"`
	AssertionConsumerService   samlMetadataAssertionConsumer `xml:"md:AssertionConsumerService"`
}

type samlMetadataAssertionConsumer struct {
	Binding   string `xml:"Binding,attr"`
	Location  string `xml:"Location,attr"`
	Index     int    `xml:"index,attr"`
	IsDefault string `xml:"isDefault,attr"`
}

func samlMetadataXML(binding SAMLBinding) ([]byte, error) {
	entity := samlMetadataEntityDescriptor{
		XMLNSMD:  "urn:oasis:names:tc:SAML:2.0:metadata",
		EntityID: binding.Audience,
		SPSSODescriptor: samlMetadataSPSSODescriptor{
			AuthnRequestsSigned:        "false",
			WantAssertionsSigned:       "true",
			ProtocolSupportEnumeration: "urn:oasis:names:tc:SAML:2.0:protocol",
			AssertionConsumerService: samlMetadataAssertionConsumer{
				Binding:   "urn:oasis:names:tc:SAML:2.0:bindings:HTTP-POST",
				Location:  binding.Audience,
				Index:     0,
				IsDefault: "true",
			},
		},
	}
	data, err := xml.MarshalIndent(entity, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal saml metadata: %w", err)
	}
	return append([]byte(xml.Header), append(data, '\n')...), nil
}

func clonePlacementCandidates(in []PlacementCandidate) []PlacementCandidate {
	if len(in) == 0 {
		return nil
	}
	out := make([]PlacementCandidate, len(in))
	copy(out, in)
	return out
}

func clonePlacementSkips(in []PlacementSkip) []PlacementSkip {
	if len(in) == 0 {
		return nil
	}
	out := make([]PlacementSkip, len(in))
	for i := range in {
		out[i] = in[i]
		out[i].MissingLabels = cloneLabels(in[i].MissingLabels)
		out[i].MissingCapabilities = cloneStrings(in[i].MissingCapabilities)
	}
	return out
}

func normalizePlacementSkips(in []PlacementSkip) []PlacementSkip {
	if len(in) == 0 {
		return nil
	}
	out := make([]PlacementSkip, 0, len(in))
	for _, skip := range in {
		skip.WorkerID = strings.TrimSpace(skip.WorkerID)
		skip.Reason = strings.TrimSpace(skip.Reason)
		skip.Status = strings.TrimSpace(skip.Status)
		skip.MissingLabels = cloneLabels(skip.MissingLabels)
		skip.MissingCapabilities = sortedUniqueStrings(skip.MissingCapabilities)
		skip.ImageRef = strings.TrimSpace(skip.ImageRef)
		skip.ImageManifestDigest = strings.TrimSpace(skip.ImageManifestDigest)
		if skip.WorkerID == "" || skip.Reason == "" {
			continue
		}
		out = append(out, skip)
	}
	return out
}

func cloneWorkerImages(in []WorkerImage) []WorkerImage {
	if len(in) == 0 {
		return nil
	}
	out := make([]WorkerImage, len(in))
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

func normalizeWorkerImageInventory(refs []string, details []WorkerImage) ([]string, []WorkerImage) {
	byRef := make(map[string]WorkerImage)
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		byRef[ref] = WorkerImage{Ref: ref}
	}
	for _, image := range details {
		ref := strings.TrimSpace(image.Ref)
		if ref == "" {
			continue
		}
		next := WorkerImage{
			Ref:                  ref,
			SourceManifestDigest: strings.TrimSpace(image.SourceManifestDigest),
		}
		cur := byRef[ref]
		if cur.Ref == "" || cur.SourceManifestDigest == "" {
			byRef[ref] = next
		}
	}
	outRefs := make([]string, 0, len(byRef))
	outDetails := make([]WorkerImage, 0, len(byRef))
	for ref, image := range byRef {
		outRefs = append(outRefs, ref)
		if image.Ref == "" {
			image.Ref = ref
		}
		outDetails = append(outDetails, image)
	}
	sort.Strings(outRefs)
	sort.Slice(outDetails, func(i, j int) bool {
		return outDetails[i].Ref < outDetails[j].Ref
	})
	return outRefs, outDetails
}

func (s *Store) selectWorkerLocked(policy, imageRef, imageManifestDigest string, labels map[string]string, capabilities []string, antiAffinityKey string, resources Capacity) (string, error) {
	if policy == "" {
		policy = PolicyLeastLoaded
	}
	switch policy {
	case PolicyLeastLoaded, PolicyImageAffinity, PolicyBinPack:
	default:
		return "", fmt.Errorf("unknown assignment policy %q", policy)
	}
	candidates := s.placementCandidatesLocked(policy, imageRef, imageManifestDigest, labels, capabilities, antiAffinityKey, resources)
	if len(candidates) == 0 {
		return "", fmt.Errorf("no ready worker matches assignment")
	}
	return candidates[0].WorkerID, nil
}

func (s *Store) placementCandidatesLocked(policy, imageRef, imageManifestDigest string, labels map[string]string, capabilities []string, antiAffinityKey string, resources Capacity) []PlacementCandidate {
	candidates, _ := s.placementEvaluationLocked(policy, imageRef, imageManifestDigest, labels, capabilities, antiAffinityKey, resources)
	return candidates
}

func (s *Store) placementEvaluationLocked(policy, imageRef, imageManifestDigest string, labels map[string]string, capabilities []string, antiAffinityKey string, resources Capacity) ([]PlacementCandidate, []PlacementSkip) {
	if policy == "" {
		policy = PolicyLeastLoaded
	}
	imageManifestDigest = strings.TrimSpace(imageManifestDigest)
	capabilities = sortedUniqueStrings(capabilities)
	resources = normalizeResources(resources)
	var candidates []PlacementCandidate
	var skipped []PlacementSkip
	for _, host := range s.sortedHostsLocked() {
		host = s.statusLocked(host)
		if host.Status != "ready" {
			skipped = append(skipped, PlacementSkip{
				WorkerID: host.ID,
				Reason:   "status",
				Status:   host.Status,
			})
			continue
		}
		if missing := missingLabels(host.Labels, labels); len(missing) > 0 {
			skipped = append(skipped, PlacementSkip{
				WorkerID:      host.ID,
				Reason:        "label",
				MissingLabels: missing,
			})
			continue
		}
		if missing := missingCapabilities(host.Capabilities, capabilities); len(missing) > 0 {
			skipped = append(skipped, PlacementSkip{
				WorkerID:            host.ID,
				Reason:              "capability",
				MissingCapabilities: missing,
			})
			continue
		}
		load := host.Capacity.VMs + s.pendingAssignmentsLocked(host.ID)
		if host.Capacity.MaxVMs > 0 && load+resources.VMs > host.Capacity.MaxVMs {
			skipped = append(skipped, PlacementSkip{
				WorkerID:     host.ID,
				Reason:       "capacity",
				Load:         load,
				MaxVMs:       host.Capacity.MaxVMs,
				RequestedVMs: resources.VMs,
			})
			continue
		}
		hasImage := workerHasImage(host, imageRef, imageManifestDigest)
		if imageManifestDigest != "" && !hasImage {
			skipped = append(skipped, PlacementSkip{
				WorkerID:            host.ID,
				Reason:              "image",
				ImageRef:            imageRef,
				ImageManifestDigest: imageManifestDigest,
			})
			continue
		}
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
	return candidates, normalizePlacementSkips(skipped)
}

func (s *Store) evacuationCandidatesLocked(workerID string, assignment Assignment) []PlacementCandidate {
	if !assignmentCanPlace(assignment) {
		return nil
	}
	candidates := s.placementCandidatesLocked(assignmentPolicy(assignment), assignment.ImageRef, assignment.ImageManifestDigest, assignment.RequiredLabels, assignment.RequiredCapabilities, assignment.AntiAffinityKey, assignment.Resources)
	out := candidates[:0]
	for _, candidate := range candidates {
		if candidate.WorkerID == workerID {
			continue
		}
		out = append(out, candidate)
	}
	return clonePlacementCandidates(out)
}

func workerHasImage(host HostRecord, imageRef, imageManifestDigest string) bool {
	imageRef = strings.TrimSpace(imageRef)
	if imageRef == "" {
		return false
	}
	imageManifestDigest = strings.TrimSpace(imageManifestDigest)
	if imageManifestDigest == "" {
		return containsString(host.ImageRefs, imageRef)
	}
	for _, image := range host.ImageDetails {
		if strings.TrimSpace(image.Ref) == imageRef && strings.TrimSpace(image.SourceManifestDigest) == imageManifestDigest {
			return true
		}
	}
	return false
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
		workerID, err := s.selectWorkerLocked(pool.Policy, pool.ImageRef, pool.ImageManifestDigest, pool.RequiredLabels, pool.RequiredCapabilities, warmPoolAntiAffinityKey(pool.Name), pool.Resources)
		if err != nil {
			return created
		}
		id := s.nextAssignmentIDLocked(now)
		assignment := Assignment{
			ID:                   id,
			Namespace:            pool.Namespace,
			WorkerID:             workerID,
			WarmPool:             pool.Name,
			Policy:               pool.Policy,
			ImageRef:             pool.ImageRef,
			ImageManifestDigest:  pool.ImageManifestDigest,
			ImageDigestRef:       pool.ImageDigestRef,
			ImagePlatform:        pool.ImagePlatform,
			RequiredLabels:       cloneLabels(pool.RequiredLabels),
			RequiredCapabilities: cloneStrings(pool.RequiredCapabilities),
			AntiAffinityKey:      warmPoolAntiAffinityKey(pool.Name),
			Resources:            pool.Resources,
			Verb:                 "cove",
			Args:                 warmPoolArgs(pool, id),
			Status:               "pending",
			Created:              now,
			Updated:              now,
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
	status := WarmPoolStatus{
		WarmPool: cloneWarmPool(pool),
		ByStatus: make(map[string]int),
	}
	for _, assignment := range s.sortedAssignmentsLocked() {
		if assignment.WarmPool != name {
			continue
		}
		assignmentStatus := normalizeOperationStatus(assignment.Status)
		addStatusCount(status.ByStatus, assignmentStatus)
		switch assignmentStatus {
		case "pending":
			status.Pending++
		case "leased":
			status.Leased++
		case "running":
			status.Running++
		case "ready":
			status.Ready++
		case "claimed":
			status.Claimed++
		case "draining":
			status.Draining++
		}
		if activeAssignmentStatus(assignmentStatus) {
			status.Active++
		}
		if openAssignmentStatus(assignmentStatus) {
			status.Slots++
			status.Assignments = append(status.Assignments, cloneAssignment(assignment))
		} else {
			status.Terminal++
		}
	}
	return status
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

func (s *Store) activeSandboxCountLocked(namespace string) int {
	namespace = normalizeNamespace(namespace)
	count := 0
	for _, assignment := range s.assignments {
		if assignment.SandboxID == "" || assignment.SandboxRole != sandboxRoleRun {
			continue
		}
		if normalizeNamespace(assignment.Namespace) != namespace {
			continue
		}
		if !sandboxTerminalStatus(assignment.Status) {
			count++
		}
	}
	return count
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

func (s *Store) sandboxStatusLocked(assignment Assignment, now time.Time) SandboxStatus {
	status := sandboxStatusFromAssignment(assignment, now)
	if sandboxPendingStopStatus(status.Status) {
		if cleanup, ok := s.activeSandboxCleanupLocked(status.ID); ok {
			status.Cleanup = &cleanup
		}
	}
	return status
}

func (s *Store) cancelSandboxWorkLocked(now time.Time, actor, id, action string) []string {
	id = strings.TrimSpace(id)
	var canceled []string
	for _, assignment := range s.sortedAssignmentsLocked() {
		if assignment.SandboxID != id {
			continue
		}
		if assignment.SandboxRole != sandboxRoleExec && assignment.SandboxRole != sandboxRoleControl {
			continue
		}
		status := normalizeOperationStatus(assignment.Status)
		if !openAssignmentStatus(status) {
			continue
		}
		workerID := assignment.WorkerID
		if workerID == "" {
			workerID = assignment.LeasedTo
		}
		if assignment.WorkerID == "" {
			assignment.WorkerID = workerID
		}
		assignment.Status = "canceled"
		assignment.LeasedTo = ""
		assignment.LeaseExpires = time.Time{}
		assignment.Updated = now
		s.assignments[assignment.ID] = assignment
		canceled = append(canceled, assignment.ID)
		fields := map[string]string{
			"reason":          action,
			"force":           "true",
			"previous_status": status,
			"operation":       "assignment.cancel",
			"sandbox_action":  action,
			"sandbox_id":      id,
			"sandbox_role":    assignment.SandboxRole,
		}
		if workerID != "" {
			fields["worker_id"] = workerID
		}
		s.appendAuditLocked(now, AuditEvent{
			Actor:        actor,
			Namespace:    assignment.Namespace,
			Action:       "assignment.cancel",
			TargetType:   "assignment",
			TargetID:     assignment.ID,
			WorkerID:     workerID,
			AssignmentID: assignment.ID,
			Status:       assignment.Status,
			Fields:       fields,
		})
	}
	return canceled
}

func (s *Store) startSandboxLocked(now time.Time, actor, id, action, holder string) (SandboxStartResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return SandboxStartResult{}, fmt.Errorf("sandbox id required")
	}
	s.reconcileLocked(now)
	assignment, ok := s.sandboxRunAssignmentLocked(id)
	if !ok {
		return SandboxStartResult{}, fmt.Errorf("sandbox %q not found", id)
	}
	if err := requireSandboxLeaseHolder(now, id, &assignment, holder); err != nil {
		return SandboxStartResult{}, err
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
		if err := s.requireSandboxWorkerReadyLocked(now, assignment.WorkerID, assignment.RequiredCapabilities); err != nil {
			return SandboxStartResult{}, err
		}
		assignment.Args = sandboxStartArgs(assignment)
	} else if assignmentCanPlace(assignment) {
		workerID, err := s.selectWorkerLocked(assignmentPolicy(assignment), assignment.ImageRef, assignment.ImageManifestDigest, assignment.RequiredLabels, assignment.RequiredCapabilities, assignment.AntiAffinityKey, assignment.Resources)
		if err != nil {
			return SandboxStartResult{}, err
		}
		assignment.WorkerID = workerID
	} else if err := s.requireSandboxWorkerReadyLocked(now, assignment.WorkerID, assignment.RequiredCapabilities); err != nil {
		return SandboxStartResult{}, err
	}
	assignment.Status = "pending"
	assignment.LeasedTo = ""
	assignment.LeaseExpires = time.Time{}
	assignment.RetryAt = time.Time{}
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

func (s *Store) requireSandboxWorkerReadyLocked(now time.Time, workerID string, capabilities []string) error {
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
	if !capabilitiesMatch(status.Capabilities, sortedUniqueStrings(capabilities)) {
		return fmt.Errorf("sandbox worker %q missing required capabilities", workerID)
	}
	return nil
}

func (s *Store) stopSandboxLocked(now time.Time, actor, id, action, holder string) (SandboxStopResult, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return SandboxStopResult{}, fmt.Errorf("sandbox id required")
	}
	s.reconcileLocked(now)
	assignment, ok := s.sandboxRunAssignmentLocked(id)
	if !ok {
		return SandboxStopResult{}, fmt.Errorf("sandbox %q not found", id)
	}
	if err := requireSandboxLeaseHolder(now, id, &assignment, holder); err != nil {
		return SandboxStopResult{}, err
	}
	vmName := SandboxAssignmentVMName(assignment)
	result := SandboxStopResult{
		Namespace:  assignment.Namespace,
		ID:         id,
		VMName:     vmName,
		Status:     assignment.Status,
		Assignment: cloneAssignment(assignment),
	}
	result.CanceledAssignments = s.cancelSandboxWorkLocked(now, actor, id, action)
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
				Priority:    assignment.Priority,
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
	fields := map[string]string{
		"vm_name":  vmName,
		"status":   result.Status,
		"canceled": strconv.FormatBool(result.Canceled),
		"cleanup":  strconv.FormatBool(result.Cleanup != nil),
	}
	if len(result.CanceledAssignments) > 0 {
		fields["canceled_assignments"] = strings.Join(result.CanceledAssignments, ",")
	}
	s.appendAuditLocked(now, AuditEvent{
		Actor:        actor,
		Namespace:    assignment.Namespace,
		Action:       action,
		TargetType:   "sandbox",
		TargetID:     id,
		WorkerID:     assignment.WorkerID,
		AssignmentID: assignment.ID,
		Fields:       fields,
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
			run.RetryAt = time.Time{}
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
	if strings.TrimSpace(req.ManifestBundle) != "" {
		return WarmPool{}, fmt.Errorf("warm pool manifest_bundle must be resolved before store admission")
	}
	pool := WarmPool{
		Namespace:            normalizeNamespace(req.Namespace),
		Name:                 strings.TrimSpace(req.Name),
		ImageRef:             strings.TrimSpace(req.ImageRef),
		ImageManifestDigest:  strings.TrimSpace(req.ImageManifestDigest),
		ImageDigestRef:       strings.TrimSpace(req.ImageDigestRef),
		ImagePlatform:        strings.TrimSpace(req.ImagePlatform),
		Size:                 req.Size,
		Policy:               strings.TrimSpace(req.Policy),
		RequiredLabels:       cloneLabels(req.RequiredLabels),
		RequiredCapabilities: sortedUniqueStrings(req.RequiredCapabilities),
		Resources:            req.Resources,
		Args:                 cloneStrings(req.Args),
		Created:              now,
		Updated:              now,
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
	pool.ImageManifestDigest = strings.TrimSpace(pool.ImageManifestDigest)
	pool.ImageDigestRef = strings.TrimSpace(pool.ImageDigestRef)
	pool.ImagePlatform = strings.TrimSpace(pool.ImagePlatform)
	pool.Policy = strings.TrimSpace(pool.Policy)
	pool.RequiredLabels = cloneLabels(pool.RequiredLabels)
	pool.RequiredCapabilities = sortedUniqueStrings(pool.RequiredCapabilities)
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
		Namespace:            assignment.Namespace,
		ID:                   assignment.SandboxID,
		VMName:               SandboxAssignmentVMName(assignment),
		ImageRef:             assignment.ImageRef,
		ImageManifestDigest:  assignment.ImageManifestDigest,
		ImageDigestRef:       assignment.ImageDigestRef,
		ImagePlatform:        assignment.ImagePlatform,
		RequiredCapabilities: cloneStrings(assignment.RequiredCapabilities),
		WorkerID:             assignment.WorkerID,
		Status:               assignment.Status,
		QueueExpires:         assignment.QueueExpires,
		MaxAttempts:          assignment.MaxAttempts,
		Attempt:              assignment.Attempt,
		RetryDelay:           assignment.RetryDelay,
		RetryAt:              assignment.RetryAt,
		Assignment:           assignment,
		Created:              assignment.Created,
		Updated:              assignment.Updated,
	}
	if normalizeOperationStatus(assignment.Status) == "pending" {
		if !assignment.Created.IsZero() {
			status.QueueAgeMillis = positiveDurationMillis(now.Sub(assignment.Created))
		}
		if !assignment.QueueExpires.IsZero() {
			status.QueueRemainingMillis = positiveDurationMillis(assignment.QueueExpires.Sub(now))
		}
		if !assignment.RetryAt.IsZero() {
			status.RetryRemainingMillis = positiveDurationMillis(assignment.RetryAt.Sub(now))
		}
	}
	if assignment.SandboxLeaseHolder != "" && !assignment.SandboxLeaseExpires.IsZero() {
		status.Lease = &SandboxLease{
			Holder:  assignment.SandboxLeaseHolder,
			Expires: assignment.SandboxLeaseExpires,
		}
	}
	return status
}

func positiveDurationMillis(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return d.Milliseconds()
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

func normalizeAssignmentQueueDeadline(now time.Time, rawTTL string, expires time.Time, name string) (time.Time, error) {
	rawTTL = strings.TrimSpace(rawTTL)
	if rawTTL != "" && !expires.IsZero() {
		return time.Time{}, fmt.Errorf("%s queue_ttl and queue_expires are mutually exclusive", name)
	}
	if rawTTL != "" {
		ttl, err := time.ParseDuration(rawTTL)
		if err != nil || ttl <= 0 {
			return time.Time{}, fmt.Errorf("%s queue_ttl must be a positive duration", name)
		}
		return now.Add(ttl).UTC(), nil
	}
	if expires.IsZero() {
		return time.Time{}, nil
	}
	expires = expires.UTC()
	if !expires.After(now) {
		return time.Time{}, fmt.Errorf("%s queue_expires must be in the future", name)
	}
	return expires, nil
}

func normalizeAssignmentRetryPolicy(maxAttempts int, rawDelay string, name string) (string, error) {
	if maxAttempts < 0 {
		return "", fmt.Errorf("%s max_attempts must be non-negative", name)
	}
	rawDelay = strings.TrimSpace(rawDelay)
	if rawDelay == "" {
		return "", nil
	}
	delay, err := time.ParseDuration(rawDelay)
	if err != nil || delay <= 0 {
		return "", fmt.Errorf("%s retry_delay must be a positive duration", name)
	}
	return rawDelay, nil
}

func normalizeAssignmentRunTimeout(rawTimeout string, name string) (string, error) {
	rawTimeout = strings.TrimSpace(rawTimeout)
	if rawTimeout == "" {
		return "", nil
	}
	timeout, err := time.ParseDuration(rawTimeout)
	if err != nil || timeout <= 0 {
		return "", fmt.Errorf("%s run_timeout must be a positive duration", name)
	}
	return rawTimeout, nil
}

func shouldRetryAssignment(assignment Assignment, status string) bool {
	return retryableAssignmentStatus(status) && assignment.MaxAttempts > 0 && assignment.Attempt < assignment.MaxAttempts
}

func retryableAssignmentStatus(status string) bool {
	return normalizeOperationStatus(status) == "failed"
}

func assignmentRetryDelay(assignment Assignment) time.Duration {
	if strings.TrimSpace(assignment.RetryDelay) == "" {
		return 0
	}
	delay, err := time.ParseDuration(assignment.RetryDelay)
	if err != nil || delay <= 0 {
		return 0
	}
	return delay
}

func sandboxMutationHolder(reqs []SandboxMutationRequest) string {
	if len(reqs) == 0 {
		return ""
	}
	return strings.TrimSpace(reqs[0].Holder)
}

func requireSandboxLeaseHolder(now time.Time, id string, assignment *Assignment, holder string) error {
	clearExpiredSandboxLease(assignment, now)
	if assignment == nil || assignment.SandboxLeaseHolder == "" {
		return nil
	}
	if strings.TrimSpace(holder) == assignment.SandboxLeaseHolder {
		return nil
	}
	return fmt.Errorf("sandbox %q lease held by %q", strings.TrimSpace(id), assignment.SandboxLeaseHolder)
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

func sandboxWaitDone(status, targetStatus string) bool {
	status = normalizeOperationStatus(status)
	targetStatus = strings.TrimSpace(targetStatus)
	if targetStatus != "" && status == targetStatus {
		return true
	}
	return sandboxTerminalStatus(status)
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

func normalizePlacementFields(a Assignment) (policy, imageRef, imageManifestDigest, antiAffinityKey string, labels map[string]string, capabilities []string, resources Capacity, err error) {
	policy = strings.TrimSpace(a.Policy)
	imageRef = strings.TrimSpace(a.ImageRef)
	imageManifestDigest = strings.TrimSpace(a.ImageManifestDigest)
	antiAffinityKey = strings.TrimSpace(a.AntiAffinityKey)
	labels = cloneLabels(a.RequiredLabels)
	capabilities = sortedUniqueStrings(a.RequiredCapabilities)
	resources, err = sanitizeResources(a.Resources)
	if err != nil {
		return "", "", "", "", nil, nil, Capacity{}, err
	}
	if strings.TrimSpace(a.ManifestBundle) != "" {
		return "", "", "", "", nil, nil, Capacity{}, fmt.Errorf("assignment manifest_bundle must be resolved before store admission")
	}
	if imageManifestDigest != "" && imageRef == "" {
		return "", "", "", "", nil, nil, Capacity{}, fmt.Errorf("assignment image_manifest_digest requires image_ref")
	}
	if policy == "" && imageRef != "" {
		policy = PolicyImageAffinity
	}
	switch policy {
	case "", PolicyLeastLoaded, PolicyImageAffinity, PolicyBinPack:
	default:
		return "", "", "", "", nil, nil, Capacity{}, fmt.Errorf("unknown assignment policy %q", policy)
	}
	return policy, imageRef, imageManifestDigest, antiAffinityKey, labels, capabilities, resources, nil
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

func auditEventMatchesSandbox(event AuditEvent, id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	if event.TargetType == "sandbox" && event.TargetID == id {
		return true
	}
	return event.Fields["sandbox_id"] == id
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

func normalizePlacementPlan(plan PlacementPlan) PlacementPlan {
	plan.ID = strings.TrimSpace(plan.ID)
	plan.Namespace = normalizeNamespace(plan.Namespace)
	plan.Policy = strings.TrimSpace(plan.Policy)
	plan.ImageRef = strings.TrimSpace(plan.ImageRef)
	plan.ImageManifestDigest = strings.TrimSpace(plan.ImageManifestDigest)
	plan.ImageDigestRef = strings.TrimSpace(plan.ImageDigestRef)
	plan.ImagePlatform = strings.TrimSpace(plan.ImagePlatform)
	plan.RequiredLabels = cloneLabels(plan.RequiredLabels)
	plan.RequiredCapabilities = sortedUniqueStrings(plan.RequiredCapabilities)
	plan.AntiAffinityKey = strings.TrimSpace(plan.AntiAffinityKey)
	plan.Resources = normalizeResources(plan.Resources)
	if !plan.Created.IsZero() {
		plan.Created = plan.Created.UTC()
	}
	if plan.Limit < 0 {
		plan.Limit = 0
	}
	plan.Candidates = clonePlacementCandidates(plan.Candidates)
	plan.Skipped = normalizePlacementSkips(plan.Skipped)
	return plan
}

func normalizeImagePrepareResult(result ImagePrepareResult) ImagePrepareResult {
	result.ID = strings.TrimSpace(result.ID)
	result.Namespace = normalizeNamespace(result.Namespace)
	result.SourceRef = strings.TrimSpace(result.SourceRef)
	result.ImageRef = strings.TrimSpace(result.ImageRef)
	result.ImageManifestDigest = strings.TrimSpace(result.ImageManifestDigest)
	result.ImageDigestRef = strings.TrimSpace(result.ImageDigestRef)
	result.ImagePlatform = strings.TrimSpace(result.ImagePlatform)
	result.RequiredLabels = cloneLabels(result.RequiredLabels)
	result.RequiredCapabilities = sortedUniqueStrings(result.RequiredCapabilities)
	if !result.Created.IsZero() {
		result.Created = result.Created.UTC()
	}
	result.Assignments = cloneAssignments(result.Assignments)
	result.Skipped = cloneImagePrepareSkips(result.Skipped)
	for i := range result.Skipped {
		result.Skipped[i].WorkerID = strings.TrimSpace(result.Skipped[i].WorkerID)
		result.Skipped[i].Reason = strings.TrimSpace(result.Skipped[i].Reason)
		result.Skipped[i].Status = strings.TrimSpace(result.Skipped[i].Status)
		result.Skipped[i].MissingLabels = cloneLabels(result.Skipped[i].MissingLabels)
		result.Skipped[i].MissingCapabilities = sortedUniqueStrings(result.Skipped[i].MissingCapabilities)
	}
	return result
}

func normalizeImageGCResult(result ImageGCResult) ImageGCResult {
	result.ID = strings.TrimSpace(result.ID)
	result.Namespace = normalizeNamespace(result.Namespace)
	result.RequiredLabels = cloneLabels(result.RequiredLabels)
	result.RequiredCapabilities = sortedUniqueStrings(result.RequiredCapabilities)
	result.OlderThan = strings.TrimSpace(result.OlderThan)
	if !result.Created.IsZero() {
		result.Created = result.Created.UTC()
	}
	result.Assignments = cloneAssignments(result.Assignments)
	result.Skipped = cloneImageGCSkips(result.Skipped)
	for i := range result.Skipped {
		result.Skipped[i].WorkerID = strings.TrimSpace(result.Skipped[i].WorkerID)
		result.Skipped[i].Reason = strings.TrimSpace(result.Skipped[i].Reason)
		result.Skipped[i].Status = strings.TrimSpace(result.Skipped[i].Status)
		result.Skipped[i].MissingLabels = cloneLabels(result.Skipped[i].MissingLabels)
		result.Skipped[i].MissingCapabilities = sortedUniqueStrings(result.Skipped[i].MissingCapabilities)
	}
	return result
}

func normalizeLifecyclePolicyResult(result LifecyclePolicyResult) LifecyclePolicyResult {
	result.ID = strings.TrimSpace(result.ID)
	result.Namespace = normalizeNamespace(result.Namespace)
	result.VMName = strings.TrimSpace(result.VMName)
	result.RequiredLabels = cloneLabels(result.RequiredLabels)
	result.RequiredCapabilities = sortedUniqueStrings(result.RequiredCapabilities)
	result.IdleTimeout = strings.TrimSpace(result.IdleTimeout)
	result.MaxAge = strings.TrimSpace(result.MaxAge)
	if result.RunBudget < 0 {
		result.RunBudget = 0
	}
	if !result.Created.IsZero() {
		result.Created = result.Created.UTC()
	}
	result.Assignments = cloneAssignments(result.Assignments)
	result.Skipped = cloneLifecyclePolicySkips(result.Skipped)
	for i := range result.Skipped {
		result.Skipped[i].WorkerID = strings.TrimSpace(result.Skipped[i].WorkerID)
		result.Skipped[i].Reason = strings.TrimSpace(result.Skipped[i].Reason)
		result.Skipped[i].Status = strings.TrimSpace(result.Skipped[i].Status)
		result.Skipped[i].MissingLabels = cloneLabels(result.Skipped[i].MissingLabels)
		result.Skipped[i].MissingCapabilities = sortedUniqueStrings(result.Skipped[i].MissingCapabilities)
	}
	return result
}

func normalizeStorageBudgetResult(result StorageBudgetResult) StorageBudgetResult {
	result.ID = strings.TrimSpace(result.ID)
	result.Namespace = normalizeNamespace(result.Namespace)
	result.RequiredLabels = cloneLabels(result.RequiredLabels)
	result.RequiredCapabilities = sortedUniqueStrings(result.RequiredCapabilities)
	result.Target = strings.TrimSpace(result.Target)
	result.WarnPct = cloneIntPtr(result.WarnPct)
	result.HardPct = cloneIntPtr(result.HardPct)
	if result.WarnPct != nil && *result.WarnPct < 0 {
		*result.WarnPct = 0
	}
	if result.HardPct != nil && *result.HardPct < 0 {
		*result.HardPct = 0
	}
	if !result.Created.IsZero() {
		result.Created = result.Created.UTC()
	}
	result.Assignments = cloneAssignments(result.Assignments)
	result.Skipped = cloneStoragePolicySkips(result.Skipped)
	for i := range result.Skipped {
		result.Skipped[i].WorkerID = strings.TrimSpace(result.Skipped[i].WorkerID)
		result.Skipped[i].Reason = strings.TrimSpace(result.Skipped[i].Reason)
		result.Skipped[i].Status = strings.TrimSpace(result.Skipped[i].Status)
		result.Skipped[i].MissingLabels = cloneLabels(result.Skipped[i].MissingLabels)
		result.Skipped[i].MissingCapabilities = sortedUniqueStrings(result.Skipped[i].MissingCapabilities)
	}
	return result
}

func normalizeStoragePruneResult(result StoragePruneResult) StoragePruneResult {
	result.ID = strings.TrimSpace(result.ID)
	result.Namespace = normalizeNamespace(result.Namespace)
	result.RequiredLabels = cloneLabels(result.RequiredLabels)
	result.RequiredCapabilities = sortedUniqueStrings(result.RequiredCapabilities)
	result.Category = strings.TrimSpace(result.Category)
	result.OlderThan = strings.TrimSpace(result.OlderThan)
	if !result.Created.IsZero() {
		result.Created = result.Created.UTC()
	}
	result.Assignments = cloneAssignments(result.Assignments)
	result.Skipped = cloneStoragePolicySkips(result.Skipped)
	for i := range result.Skipped {
		result.Skipped[i].WorkerID = strings.TrimSpace(result.Skipped[i].WorkerID)
		result.Skipped[i].Reason = strings.TrimSpace(result.Skipped[i].Reason)
		result.Skipped[i].Status = strings.TrimSpace(result.Skipped[i].Status)
		result.Skipped[i].MissingLabels = cloneLabels(result.Skipped[i].MissingLabels)
		result.Skipped[i].MissingCapabilities = sortedUniqueStrings(result.Skipped[i].MissingCapabilities)
	}
	return result
}

func controllerRunFromPlacementPlan(plan PlacementPlan) ControllerRunSummary {
	fields := map[string]string{
		"policy":     plan.Policy,
		"candidates": strconv.Itoa(len(plan.Candidates)),
		"limit":      strconv.Itoa(plan.Limit),
	}
	targetType := "placement"
	targetID := plan.Policy
	if plan.ImageRef != "" {
		targetType = "image"
		targetID = plan.ImageRef
		fields["image_ref"] = plan.ImageRef
	}
	if plan.ImageManifestDigest != "" {
		fields["image_manifest_digest"] = plan.ImageManifestDigest
	}
	if plan.ImageDigestRef != "" {
		fields["image_digest_ref"] = plan.ImageDigestRef
	}
	if plan.ImagePlatform != "" {
		fields["image_platform"] = plan.ImagePlatform
	}
	if plan.AntiAffinityKey != "" {
		fields["anti_affinity_key"] = plan.AntiAffinityKey
	}
	if len(plan.RequiredCapabilities) > 0 {
		fields["required_capabilities"] = strings.Join(plan.RequiredCapabilities, ",")
	}
	return ControllerRunSummary{
		ID:             plan.ID,
		Created:        plan.Created.UTC(),
		Namespace:      plan.Namespace,
		Kind:           ControllerRunKindPlacementPlan,
		TargetType:     targetType,
		TargetID:       targetID,
		CandidateCount: len(plan.Candidates),
		SkipCount:      len(plan.Skipped),
		Fields:         fields,
	}
}

func controllerRunFromImagePrepare(prep ImagePrepareResult) ControllerRunSummary {
	fields := map[string]string{
		"source_ref": prep.SourceRef,
		"image_ref":  prep.ImageRef,
	}
	if prep.ImageManifestDigest != "" {
		fields["image_manifest_digest"] = prep.ImageManifestDigest
	}
	if prep.ImageDigestRef != "" {
		fields["image_digest_ref"] = prep.ImageDigestRef
	}
	if prep.ImagePlatform != "" {
		fields["image_platform"] = prep.ImagePlatform
	}
	if len(prep.RequiredCapabilities) > 0 {
		fields["required_capabilities"] = strings.Join(prep.RequiredCapabilities, ",")
	}
	return ControllerRunSummary{
		ID:              prep.ID,
		Created:         prep.Created.UTC(),
		Namespace:       prep.Namespace,
		Kind:            ControllerRunKindImagePrepare,
		TargetType:      "image",
		TargetID:        prep.ImageRef,
		AssignmentCount: len(prep.Assignments),
		SkipCount:       len(prep.Skipped),
		Fields:          fields,
	}
}

func controllerRunFromImageGC(run ImageGCResult) ControllerRunSummary {
	fields := map[string]string{
		"apply":      strconv.FormatBool(run.Apply),
		"older_than": run.OlderThan,
	}
	if len(run.RequiredCapabilities) > 0 {
		fields["required_capabilities"] = strings.Join(run.RequiredCapabilities, ",")
	}
	return ControllerRunSummary{
		ID:              run.ID,
		Created:         run.Created.UTC(),
		Namespace:       run.Namespace,
		Kind:            ControllerRunKindImageGC,
		TargetType:      "image",
		AssignmentCount: len(run.Assignments),
		SkipCount:       len(run.Skipped),
		Fields:          fields,
	}
}

func controllerRunFromLifecyclePolicy(run LifecyclePolicyResult) ControllerRunSummary {
	fields := map[string]string{
		"clear": strconv.FormatBool(run.Clear),
	}
	if run.IdleTimeout != "" {
		fields["idle_timeout"] = run.IdleTimeout
	}
	if run.MaxAge != "" {
		fields["max_age"] = run.MaxAge
	}
	if run.RunBudget > 0 {
		fields["run_budget"] = strconv.Itoa(run.RunBudget)
	}
	if len(run.RequiredCapabilities) > 0 {
		fields["required_capabilities"] = strings.Join(run.RequiredCapabilities, ",")
	}
	return ControllerRunSummary{
		ID:              run.ID,
		Created:         run.Created.UTC(),
		Namespace:       run.Namespace,
		Kind:            ControllerRunKindLifecyclePolicy,
		TargetType:      "vm",
		TargetID:        run.VMName,
		AssignmentCount: len(run.Assignments),
		SkipCount:       len(run.Skipped),
		Fields:          fields,
	}
}

func controllerRunFromStorageBudget(run StorageBudgetResult) ControllerRunSummary {
	fields := map[string]string{
		"clear": strconv.FormatBool(run.Clear),
	}
	if run.Target != "" {
		fields["target"] = run.Target
	}
	if run.WarnPct != nil {
		fields["warn_pct"] = strconv.Itoa(*run.WarnPct)
	}
	if run.HardPct != nil {
		fields["hard_pct"] = strconv.Itoa(*run.HardPct)
	}
	if len(run.RequiredCapabilities) > 0 {
		fields["required_capabilities"] = strings.Join(run.RequiredCapabilities, ",")
	}
	return ControllerRunSummary{
		ID:              run.ID,
		Created:         run.Created.UTC(),
		Namespace:       run.Namespace,
		Kind:            ControllerRunKindStorageBudget,
		TargetType:      "storage",
		AssignmentCount: len(run.Assignments),
		SkipCount:       len(run.Skipped),
		Fields:          fields,
	}
}

func controllerRunFromStoragePrune(run StoragePruneResult) ControllerRunSummary {
	fields := map[string]string{
		"apply": strconv.FormatBool(run.Apply),
	}
	if run.Category != "" {
		fields["category"] = run.Category
	}
	if run.OlderThan != "" {
		fields["older_than"] = run.OlderThan
	}
	if len(run.RequiredCapabilities) > 0 {
		fields["required_capabilities"] = strings.Join(run.RequiredCapabilities, ",")
	}
	return ControllerRunSummary{
		ID:              run.ID,
		Created:         run.Created.UTC(),
		Namespace:       run.Namespace,
		Kind:            ControllerRunKindStoragePrune,
		TargetType:      "storage",
		AssignmentCount: len(run.Assignments),
		SkipCount:       len(run.Skipped),
		Fields:          fields,
	}
}

func normalizeAssignmentReport(report AssignmentReport) AssignmentReport {
	report.Namespace = normalizeNamespace(report.Namespace)
	report.AssignmentID = strings.TrimSpace(report.AssignmentID)
	report.WorkerID = strings.TrimSpace(report.WorkerID)
	report.Status = strings.TrimSpace(report.Status)
	if !report.Created.IsZero() {
		report.Created = report.Created.UTC()
	}
	if !report.Updated.IsZero() {
		report.Updated = report.Updated.UTC()
	}
	report.Report.ID = strings.TrimSpace(report.Report.ID)
	report.Report.AssignmentID = strings.TrimSpace(report.Report.AssignmentID)
	report.Report.Status = strings.TrimSpace(report.Report.Status)
	if !report.Report.Time.IsZero() {
		report.Report.Time = report.Report.Time.UTC()
	}
	if report.AssignmentID == "" {
		report.AssignmentID = report.Report.AssignmentID
	}
	if report.WorkerID == "" {
		report.WorkerID = report.Report.ID
	}
	if report.Status == "" {
		report.Status = report.Report.Status
	}
	return report
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

func applySAMLBindingMetadataRequest(record *samlBindingRecord, req SAMLBindingRequest, now time.Time) error {
	if raw := strings.TrimSpace(req.MetadataXML); raw != "" {
		metadata, err := parseSAMLIDPMetadata([]byte(raw))
		if err != nil {
			return err
		}
		applySAMLIDPMetadata(record, metadata)
	}
	if strings.TrimSpace(record.MetadataURL) != "" {
		return applySAMLBindingMetadataURL(record, now)
	}
	return nil
}

func applySAMLBindingMetadataURL(record *samlBindingRecord, now time.Time) error {
	data, metadataURL, err := fetchSAMLIDPMetadata(record.MetadataURL)
	if err != nil {
		return err
	}
	metadata, err := parseSAMLIDPMetadata(data)
	if err != nil {
		return err
	}
	applySAMLIDPMetadata(record, metadata)
	record.MetadataURL = metadataURL
	record.MetadataFetched = now.UTC()
	return nil
}

func applySAMLIDPMetadata(record *samlBindingRecord, metadata samlIDPMetadata) {
	record.EntityID = metadata.EntityID
	record.SSOURL = metadata.SSOURL
	record.CertificatePEM = metadata.CertificatePEM
}

func normalizeSAMLBindingRecord(record samlBindingRecord) (samlBindingRecord, error) {
	record.Name = strings.TrimSpace(record.Name)
	if record.Name == "" {
		return samlBindingRecord{}, fmt.Errorf("saml binding name required")
	}
	record.EntityID = strings.TrimSpace(record.EntityID)
	if record.EntityID == "" {
		return samlBindingRecord{}, fmt.Errorf("saml binding entity_id required")
	}
	record.Subject = strings.TrimSpace(record.Subject)
	record.SSOURL = strings.TrimSpace(record.SSOURL)
	if record.SSOURL == "" {
		return samlBindingRecord{}, fmt.Errorf("saml binding sso_url required")
	}
	var err error
	record.SSOURL, err = normalizeOIDCURL(record.SSOURL, "saml binding sso_url")
	if err != nil {
		return samlBindingRecord{}, err
	}
	record.Audience = strings.TrimSpace(record.Audience)
	if record.Audience == "" {
		return samlBindingRecord{}, fmt.Errorf("saml binding audience required")
	}
	if strings.TrimSpace(record.Role) == "" {
		return samlBindingRecord{}, fmt.Errorf("saml binding role required")
	}
	role, err := normalizeServiceAccountRole(record.Role)
	if err != nil {
		return samlBindingRecord{}, err
	}
	record.Role = role
	record.Namespace = normalizeNamespace(record.Namespace)
	if strings.TrimSpace(record.MetadataURL) != "" {
		record.MetadataURL, err = normalizeOIDCURL(record.MetadataURL, "saml binding metadata_url")
		if err != nil {
			return samlBindingRecord{}, err
		}
	}
	if !record.MetadataFetched.IsZero() {
		record.MetadataFetched = record.MetadataFetched.UTC()
	}
	record.CertificatePEM, err = normalizeSAMLCertificatePEM(record.CertificatePEM)
	if err != nil {
		return samlBindingRecord{}, err
	}
	if !record.Created.IsZero() {
		record.Created = record.Created.UTC()
	}
	if !record.Updated.IsZero() {
		record.Updated = record.Updated.UTC()
	}
	return record, nil
}

func normalizeSAMLReplayRecord(record samlReplayRecord) samlReplayRecord {
	record.Binding = strings.TrimSpace(record.Binding)
	record.AssertionID = strings.TrimSpace(record.AssertionID)
	if !record.Expires.IsZero() {
		record.Expires = record.Expires.UTC()
	}
	return record
}

func normalizeSAMLSessionRecord(record samlSessionRecord) samlSessionRecord {
	record.TokenHash = strings.TrimSpace(record.TokenHash)
	record.Binding = strings.TrimSpace(record.Binding)
	record.Subject = strings.TrimSpace(record.Subject)
	record.Namespace = normalizeNamespace(record.Namespace)
	record.Role = strings.TrimSpace(record.Role)
	if !record.Expires.IsZero() {
		record.Expires = record.Expires.UTC()
	}
	if !record.Created.IsZero() {
		record.Created = record.Created.UTC()
	}
	if !record.Updated.IsZero() {
		record.Updated = record.Updated.UTC()
	}
	return record
}

func normalizeSAMLCertificatePEM(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("saml binding certificate_pem required")
	}
	block, _ := pem.Decode([]byte(raw))
	if block == nil {
		return "", fmt.Errorf("saml binding certificate_pem must contain a PEM certificate")
	}
	if block.Type != "CERTIFICATE" {
		return "", fmt.Errorf("saml binding certificate_pem must contain a CERTIFICATE block")
	}
	if _, err := x509.ParseCertificate(block.Bytes); err != nil {
		return "", fmt.Errorf("saml binding certificate_pem: %w", err)
	}
	return strings.TrimSpace(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: block.Bytes}))), nil
}

func samlCertificateSHA256(certPEM string) string {
	block, _ := pem.Decode([]byte(strings.TrimSpace(certPEM)))
	if block == nil {
		return ""
	}
	sum := sha256.Sum256(block.Bytes)
	return hex.EncodeToString(sum[:])
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

func missingLabels(have, want map[string]string) map[string]string {
	if len(want) == 0 {
		return nil
	}
	missing := make(map[string]string)
	for key, value := range want {
		if have[key] != value {
			missing[key] = value
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return missing
}

func capabilitiesMatch(have, want []string) bool {
	for _, capability := range want {
		if !containsString(have, capability) {
			return false
		}
	}
	return true
}

func missingCapabilities(have, want []string) []string {
	if len(want) == 0 {
		return nil
	}
	var missing []string
	for _, capability := range sortedUniqueStrings(want) {
		if !containsString(have, capability) {
			missing = append(missing, capability)
		}
	}
	return missing
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
