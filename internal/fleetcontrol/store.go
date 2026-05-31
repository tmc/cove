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
	mu    sync.Mutex
	path  string
	ttl   time.Duration
	now   func() time.Time
	hosts map[string]HostRecord
}

type storeFile struct {
	Hosts []HostRecord `json:"hosts"`
}

func OpenStore(path string, ttl time.Duration) (*Store, error) {
	if ttl <= 0 {
		ttl = DefaultWorkerTTL
	}
	s := &Store{
		path:  strings.TrimSpace(path),
		ttl:   ttl,
		now:   time.Now,
		hosts: make(map[string]HostRecord),
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
		s.hosts[id] = host
	}
	return s, nil
}

func NewMemoryStore(ttl time.Duration) *Store {
	s, _ := OpenStore("", ttl)
	return s
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
		ID:       id,
		Host:     strings.TrimSpace(h.Host),
		Address:  strings.TrimSpace(h.Address),
		Version:  strings.TrimSpace(h.Version),
		Labels:   cloneLabels(h.Labels),
		Capacity: h.Capacity,
		Status:   "ready",
		LastSeen: now,
		Expires:  now.Add(s.ttl),
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
	record.Report = &r
	record.Status = status
	s.hosts[id] = record
	if err := s.persistLocked(); err != nil {
		return HostRecord{}, err
	}
	return s.statusLocked(record), nil
}

func (s *Store) AwaitAssignment(id string) (*Assignment, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("worker id required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.hosts[id]; !ok {
		return nil, fmt.Errorf("worker %q not registered", id)
	}
	return nil, nil
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
	data, err := json.MarshalIndent(storeFile{Hosts: hosts}, "", "  ")
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
