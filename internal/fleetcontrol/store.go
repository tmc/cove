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
}

type storeFile struct {
	Hosts       []HostRecord `json:"hosts"`
	Assignments []Assignment `json:"assignments,omitempty"`
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
		assignment.ImageRef = strings.TrimSpace(assignment.ImageRef)
		assignment.RequiredLabels = cloneLabels(assignment.RequiredLabels)
		if assignment.Status == "" {
			assignment.Status = "pending"
		}
		s.assignments[id] = assignment
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

func (s *Store) UpsertHeartbeat(h WorkerHeartbeat) (HostRecord, error) {
	id := strings.TrimSpace(h.ID)
	if id == "" {
		return HostRecord{}, fmt.Errorf("worker id required")
	}
	if h.CPUs < 0 || h.VMs < 0 || h.Images < 0 {
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
	if old, ok := s.hosts[id]; ok && record.Host == "" {
		record.Host = old.Host
	}
	s.hosts[id] = record
	if err := s.persistLocked(); err != nil {
		return HostRecord{}, err
	}
	return record, nil
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
	if r.Time.IsZero() {
		r.Time = s.now().UTC()
	} else {
		r.Time = r.Time.UTC()
	}

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
			if assignment.LeasedTo != "" && assignment.LeasedTo != id {
				return HostRecord{}, fmt.Errorf("assignment %q leased to %q", r.AssignmentID, assignment.LeasedTo)
			}
		}
	}
	record.Report = &r
	record.Status = status
	s.hosts[id] = record
	if r.AssignmentID != "" {
		assignment, ok := s.assignments[r.AssignmentID]
		if ok {
			assignment.Status = status
			assignment.Updated = r.Time
			assignment.LastReport = &r
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
	policy := strings.TrimSpace(a.Policy)
	imageRef := strings.TrimSpace(a.ImageRef)
	requiredLabels := cloneLabels(a.RequiredLabels)
	if policy == "" && imageRef != "" {
		policy = PolicyImageAffinity
	}
	if policy != "" && policy != PolicyLeastLoaded && policy != PolicyImageAffinity {
		return Assignment{}, fmt.Errorf("unknown assignment policy %q", policy)
	}
	now := s.now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()
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
		selected, err := s.selectWorkerLocked(policy, imageRef, requiredLabels)
		if err != nil {
			return Assignment{}, err
		}
		workerID = selected
	}
	a.ID = id
	a.WorkerID = workerID
	a.Policy = policy
	a.ImageRef = imageRef
	a.RequiredLabels = requiredLabels
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
	assignments := s.sortedAssignmentsLocked()
	for _, assignment := range assignments {
		if assignment.WorkerID != "" && assignment.WorkerID != id {
			continue
		}
		if assignment.Status != "pending" && !(assignment.Status == "leased" && now.After(assignment.LeaseExpires)) {
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
	out := make([]HostRecord, 0, len(s.hosts))
	for _, host := range s.hosts {
		out = append(out, s.statusLocked(host))
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out
}

func (s *Store) statusLocked(record HostRecord) HostRecord {
	if s.now().After(record.Expires) {
		record.Status = "stale"
	}
	record.Labels = cloneLabels(record.Labels)
	record.ImageRefs = cloneStrings(record.ImageRefs)
	if record.Report != nil {
		report := *record.Report
		record.Report = &report
	}
	return record
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
	data, err := json.MarshalIndent(storeFile{Hosts: hosts, Assignments: assignments}, "", "  ")
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

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func (s *Store) selectWorkerLocked(policy, imageRef string, labels map[string]string) (string, error) {
	if policy == "" {
		policy = PolicyLeastLoaded
	}
	switch policy {
	case PolicyLeastLoaded, PolicyImageAffinity:
	default:
		return "", fmt.Errorf("unknown assignment policy %q", policy)
	}
	var best HostRecord
	bestSet := false
	bestLoad := 0
	bestHasImage := false
	for _, host := range s.hosts {
		host = s.statusLocked(host)
		if host.Status != "ready" {
			continue
		}
		if !labelsMatch(host.Labels, labels) {
			continue
		}
		load := host.Capacity.VMs + s.pendingAssignmentsLocked(host.ID)
		hasImage := imageRef != "" && containsString(host.ImageRefs, imageRef)
		if !bestSet {
			best = host
			bestSet = true
			bestLoad = load
			bestHasImage = hasImage
			continue
		}
		if betterHost(policy, host.ID, load, hasImage, best.ID, bestLoad, bestHasImage) {
			best = host
			bestLoad = load
			bestHasImage = hasImage
		}
	}
	if !bestSet {
		return "", fmt.Errorf("no ready worker matches assignment")
	}
	return best.ID, nil
}

func betterHost(policy, id string, load int, hasImage bool, bestID string, bestLoad int, bestHasImage bool) bool {
	if policy == PolicyImageAffinity && hasImage != bestHasImage {
		return hasImage
	}
	if load != bestLoad {
		return load < bestLoad
	}
	return id < bestID
}

func (s *Store) pendingAssignmentsLocked(workerID string) int {
	var n int
	for _, assignment := range s.assignments {
		if assignment.WorkerID != workerID && assignment.LeasedTo != workerID {
			continue
		}
		switch assignment.Status {
		case "pending", "leased":
			n++
		}
	}
	return n
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
