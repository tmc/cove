package fleetcontrol

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
}

type storeFile struct {
	Hosts       []HostRecord `json:"hosts"`
	Assignments []Assignment `json:"assignments,omitempty"`
	WarmPools   []WarmPool   `json:"warm_pools,omitempty"`
}

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
		assignment.Args = cloneStrings(assignment.Args)
		assignment.WarmPool = strings.TrimSpace(assignment.WarmPool)
		assignment.WarmPoolSlot = strings.TrimSpace(assignment.WarmPoolSlot)
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
	now := s.now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()
	result := s.reconcileLocked(now)
	if !result.changed() {
		return result, nil
	}
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
	if err := s.persistLocked(); err != nil {
		return HostRecord{}, err
	}
	return record, nil
}

func (s *Store) CordonWorker(id, reason string) (HostRecord, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return HostRecord{}, fmt.Errorf("worker id required")
	}
	now := s.now().UTC()
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
	if err := s.persistLocked(); err != nil {
		return HostRecord{}, err
	}
	return s.statusLocked(record), nil
}

func (s *Store) UncordonWorker(id string) (HostRecord, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return HostRecord{}, fmt.Errorf("worker id required")
	}
	now := s.now().UTC()
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
			assignment.Status = storedStatus
			assignment.Updated = received
			assignment.LastReport = &r
			if assignmentLeaseStatus(status) {
				assignment.LeasedTo = id
				assignment.LeaseExpires = received.Add(s.assignmentTTL)
			}
			s.assignments[assignment.ID] = assignment
		}
	}
	if err := s.persistLocked(); err != nil {
		return HostRecord{}, err
	}
	return s.statusLocked(record), nil
}

func (s *Store) CreateAssignment(a Assignment) (Assignment, error) {
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
	a.WorkerID = workerID
	a.WarmPool = strings.TrimSpace(a.WarmPool)
	a.WarmPoolSlot = strings.TrimSpace(a.WarmPoolSlot)
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
	now := s.now().UTC()
	pool, err := warmPoolFromRequest(req, now)
	if err != nil {
		return WarmPoolResult{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.warmPools[pool.Name]; ok && !existing.Created.IsZero() {
		pool.Created = existing.Created
	}
	s.warmPools[pool.Name] = pool
	created := s.ensureWarmPoolLocked(now, pool)
	status := s.warmPoolStatusLocked(pool.Name)
	if err := s.persistLocked(); err != nil {
		return WarmPoolResult{Pool: status}, err
	}
	return WarmPoolResult{Pool: status, Created: cloneAssignments(created)}, nil
}

func (s *Store) ClaimWarmPool(req WarmPoolClaimRequest) (WarmPoolClaimResult, error) {
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

	s.mu.Lock()
	defer s.mu.Unlock()
	s.reconcileLocked(now)
	pool, ok := s.warmPools[name]
	if !ok {
		return WarmPoolClaimResult{}, fmt.Errorf("warm pool %q not found", name)
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
	if err := s.persistLocked(); err != nil {
		return WarmPoolClaimResult{}, err
	}
	return WarmPoolClaimResult{
		Pool:       name,
		VMName:     vmName,
		Slot:       cloneAssignment(slot),
		Assignment: cloneAssignment(assignment),
	}, nil
}

func (s *Store) PrepareImage(req ImagePrepareRequest) (ImagePrepareResult, error) {
	sourceRef := strings.TrimSpace(req.SourceRef)
	imageRef := strings.TrimSpace(req.ImageRef)
	if sourceRef == "" {
		return ImagePrepareResult{}, fmt.Errorf("image prepare source_ref required")
	}
	if imageRef == "" {
		return ImagePrepareResult{}, fmt.Errorf("image prepare image_ref required")
	}
	labels := cloneLabels(req.RequiredLabels)
	now := s.now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()
	reconciled := s.reconcileLocked(now)
	result := ImagePrepareResult{
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
	s.mu.Lock()
	defer s.mu.Unlock()
	assignments := s.sortedAssignmentsLocked()
	for i := range assignments {
		assignments[i] = cloneAssignment(assignments[i])
	}
	return assignments
}

func (s *Store) ListWarmPools() []WarmPoolStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	pools := s.sortedWarmPoolsLocked()
	out := make([]WarmPoolStatus, 0, len(pools))
	for _, pool := range pools {
		out = append(out, s.warmPoolStatusLocked(pool.Name))
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
		created := s.ensureWarmPoolLocked(now, pool)
		for _, assignment := range created {
			result.WarmPoolAssignments = append(result.WarmPoolAssignments, assignment.ID)
		}
	}
	sort.Strings(result.StaleWorkers)
	sort.Strings(result.RequeuedAssignments)
	sort.Strings(result.ReplacedAssignments)
	sort.Strings(result.WarmPoolAssignments)
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
	return len(r.StaleWorkers) > 0 || len(r.RequeuedAssignments) > 0 || len(r.ReplacedAssignments) > 0 || len(r.WarmPoolAssignments) > 0
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
	return activeAssignmentStatus(status) || status == "claimed"
}

func assignmentLeaseStatus(status string) bool {
	switch status {
	case "leased", "running", "ready", "claimed":
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
	data, err := json.MarshalIndent(storeFile{Hosts: hosts, Assignments: assignments, WarmPools: warmPools}, "", "  ")
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

func warmPoolFromRequest(req WarmPoolRequest, now time.Time) (WarmPool, error) {
	pool := WarmPool{
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
	pool.ImageRef = strings.TrimSpace(pool.ImageRef)
	pool.Policy = strings.TrimSpace(pool.Policy)
	pool.RequiredLabels = cloneLabels(pool.RequiredLabels)
	pool.Args = cloneStrings(pool.Args)
	if err := validateWarmPoolArgs(pool.Args); err != nil {
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
	return warmPoolForkName(assignment.WarmPool, assignment.ID)
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

func validateWarmPoolArgs(args []string) error {
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
			return fmt.Errorf("warm pool args must not set %s", flag)
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
	if assignment.WarmPoolSlot != "" {
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

func imagePrepareArgs(sourceRef, imageRef string, force bool) []string {
	args := []string{"image", "pull", "-tag", imageRef}
	if force {
		args = append(args, "-force")
	}
	return append(args, sourceRef)
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
