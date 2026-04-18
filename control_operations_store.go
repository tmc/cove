package main

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Operation represents a long-running operation persisted to disk.
type Operation struct {
	ID        string             `json:"id"`
	Resource  string             `json:"resource"`
	Status    string             `json:"status"` // pending|running|succeeded|failed
	CreatedAt time.Time          `json:"created_at"`
	UpdatedAt time.Time          `json:"updated_at"`
	Progress  *OperationProgress `json:"progress,omitempty"`
	Result    map[string]any     `json:"result,omitempty"`
	Error     *OperationError    `json:"error,omitempty"`
}

// OperationProgress holds phase and percent-complete for a running operation.
type OperationProgress struct {
	Phase   string `json:"phase"`
	Percent int    `json:"percent"`
	Message string `json:"message,omitempty"`
}

// OperationError holds a machine-readable code and human-readable message.
type OperationError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// OperationStore persists Operation records.
type OperationStore interface {
	Save(op *Operation) error
	Load() ([]*Operation, error)
	Delete(id string) error
	// PurgeOlderThan deletes terminal (succeeded/failed) operations older than d.
	// Returns count of purged operations.
	PurgeOlderThan(d time.Duration) (int, error)
}

// FileOperationStore persists operations as JSON files in a directory.
// Each operation is stored as <id>.json with atomic write-temp-then-rename.
type FileOperationStore struct {
	dir string
	mu  sync.Mutex
}

// NewFileOperationStore creates a FileOperationStore rooted at dir.
// dir is created with mode 0700 if it does not exist.
func NewFileOperationStore(dir string) (*FileOperationStore, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create operations dir: %w", err)
	}
	return &FileOperationStore{dir: dir}, nil
}

// Save writes op to <dir>/<op.ID>.json atomically.
func (s *FileOperationStore) Save(op *Operation) error {
	data, err := json.Marshal(op)
	if err != nil {
		return fmt.Errorf("marshal operation: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	tmpPath := filepath.Join(s.dir, op.ID+".json.tmp")
	finalPath := filepath.Join(s.dir, op.ID+".json")

	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("open tmp file: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("write operation: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("fsync operation file: %w", err)
	}
	f.Close()

	if err := os.Rename(tmpPath, finalPath); err != nil {
		return fmt.Errorf("rename operation file: %w", err)
	}

	// On darwin, fsync the parent directory to make the rename durable.
	if err := s.syncDir(); err != nil {
		return fmt.Errorf("fsync operations dir: %w", err)
	}
	return nil
}

// syncDir opens and fsyncs the store directory to flush directory entry changes.
func (s *FileOperationStore) syncDir() error {
	d, err := os.Open(s.dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// Load reads all *.json files from the store directory.
// Any operation with status "pending" or "running" (orphaned from a prior process)
// is re-written as "failed" with code "server_restart" before being returned.
// The returned slice is sorted by CreatedAt ascending.
func (s *FileOperationStore) Load() ([]*Operation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var ops []*Operation

	err := filepath.WalkDir(s.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && path != s.dir {
			return fs.SkipDir
		}
		name := d.Name()
		if filepath.Ext(name) != ".json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		var op Operation
		if err := json.Unmarshal(data, &op); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		ops = append(ops, &op)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("load operations: %w", err)
	}

	now := time.Now()
	for _, op := range ops {
		if op.Status == "pending" || op.Status == "running" {
			op.Status = "failed"
			op.UpdatedAt = now
			op.Error = &OperationError{
				Code:    "server_restart",
				Message: "server restarted mid-operation",
			}
			// write-through without holding a second lock (already locked)
			if err := s.saveUnlocked(op); err != nil {
				return nil, fmt.Errorf("rewrite orphaned op %s: %w", op.ID, err)
			}
		}
	}

	sort.Slice(ops, func(i, j int) bool {
		return ops[i].CreatedAt.Before(ops[j].CreatedAt)
	})
	return ops, nil
}

// saveUnlocked is Save without acquiring the mutex (caller must hold it).
func (s *FileOperationStore) saveUnlocked(op *Operation) error {
	data, err := json.Marshal(op)
	if err != nil {
		return fmt.Errorf("marshal operation: %w", err)
	}
	tmpPath := filepath.Join(s.dir, op.ID+".json.tmp")
	finalPath := filepath.Join(s.dir, op.ID+".json")

	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("open tmp file: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("write operation: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("fsync operation file: %w", err)
	}
	f.Close()

	if err := os.Rename(tmpPath, finalPath); err != nil {
		return fmt.Errorf("rename operation file: %w", err)
	}
	return s.syncDir()
}

// Delete removes the JSON file for the given operation ID.
func (s *FileOperationStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := filepath.Join(s.dir, id+".json")
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("delete operation %s: %w", id, err)
	}
	return nil
}

// PurgeOlderThan deletes terminal (succeeded/failed) operations whose UpdatedAt
// is older than d. Pending/running operations are never removed.
// Returns the count of purged operations.
func (s *FileOperationStore) PurgeOlderThan(d time.Duration) (int, error) {
	cutoff := time.Now().Add(-d)

	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return 0, fmt.Errorf("read operations dir: %w", err)
	}

	purged := 0
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(s.dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var op Operation
		if err := json.Unmarshal(data, &op); err != nil {
			continue
		}
		if op.Status != "succeeded" && op.Status != "failed" {
			continue
		}
		if op.UpdatedAt.After(cutoff) {
			continue
		}
		if err := os.Remove(path); err == nil {
			purged++
		}
	}
	return purged, nil
}

// MemOperationStore is an in-memory OperationStore for use in tests.
type MemOperationStore struct {
	mu  sync.Mutex
	ops map[string]*Operation
}

// NewMemOperationStore returns an empty in-memory store.
func NewMemOperationStore() *MemOperationStore {
	return &MemOperationStore{ops: make(map[string]*Operation)}
}

func (m *MemOperationStore) Save(op *Operation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *op
	m.ops[op.ID] = &cp
	return nil
}

func (m *MemOperationStore) Load() ([]*Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Operation, 0, len(m.ops))
	for _, op := range m.ops {
		cp := *op
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (m *MemOperationStore) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.ops[id]; !ok {
		return fmt.Errorf("operation %s not found", id)
	}
	delete(m.ops, id)
	return nil
}

func (m *MemOperationStore) PurgeOlderThan(d time.Duration) (int, error) {
	cutoff := time.Now().Add(-d)
	m.mu.Lock()
	defer m.mu.Unlock()
	purged := 0
	for id, op := range m.ops {
		if op.Status != "succeeded" && op.Status != "failed" {
			continue
		}
		if op.UpdatedAt.Before(cutoff) {
			delete(m.ops, id)
			purged++
		}
	}
	return purged, nil
}
