package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// ErrOperationNotFound is returned when an operation ID is not found in the registry.
var ErrOperationNotFound = errors.New("operation not found")

// isTerminal reports whether status is a terminal (non-progressing) state.
func isTerminal(status string) bool {
	return status == "succeeded" || status == "failed"
}

// opEntry holds the in-memory state for one operation plus its subscriber list.
type opEntry struct {
	op   *Operation
	subs []chan *Operation
}

// OperationRegistry is a concurrency-safe registry for long-running operations.
// It indexes operations in memory, persists them via an OperationStore, and
// broadcasts state changes to SSE subscribers.
type OperationRegistry struct {
	store OperationStore
	mu    sync.Mutex
	ops   map[string]*opEntry
}

// NewOperationRegistry creates a registry backed by store.
// It calls store.Load() to re-index operations from a prior process.
func NewOperationRegistry(store OperationStore) (*OperationRegistry, error) {
	r := &OperationRegistry{
		store: store,
		ops:   make(map[string]*opEntry),
	}
	ops, err := store.Load()
	if err != nil {
		return nil, fmt.Errorf("load operations: %w", err)
	}
	for _, op := range ops {
		cp := *op
		r.ops[op.ID] = &opEntry{op: &cp}
	}
	return r, nil
}

// newOpID generates an op_<8 hex chars> identifier from 4 random bytes.
func newOpID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate op id: %w", err)
	}
	return "op_" + hex.EncodeToString(b), nil
}

// Create allocates a new operation for resource, persists it as "pending", and
// returns a pointer to the stored copy.
func (r *OperationRegistry) Create(resource string) (*Operation, error) {
	id, err := newOpID()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	op := &Operation{
		ID:        id,
		Resource:  resource,
		Status:    "pending",
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := r.store.Save(op); err != nil {
		return nil, fmt.Errorf("persist new operation: %w", err)
	}
	cp := *op
	r.mu.Lock()
	r.ops[id] = &opEntry{op: &cp}
	r.mu.Unlock()
	ret := *op
	return &ret, nil
}

// Get returns a snapshot of the operation with the given id, or false if not found.
func (r *OperationRegistry) Get(id string) (*Operation, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.ops[id]
	if !ok {
		return nil, false
	}
	cp := *e.op
	return &cp, true
}

// List returns snapshots of all operations sorted by CreatedAt ascending.
func (r *OperationRegistry) List() []*Operation {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*Operation, 0, len(r.ops))
	for _, e := range r.ops {
		cp := *e.op
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out
}

// Update applies mutator to a copy of the named operation, persists it, and
// broadcasts a snapshot to all subscribers. On terminal status the subscriber
// channels are closed. If persist fails the in-memory state is not updated.
func (r *OperationRegistry) Update(id string, mutator func(*Operation)) error {
	r.mu.Lock()
	e, ok := r.ops[id]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("%w: %s", ErrOperationNotFound, id)
	}
	// Apply mutator to a working copy.
	updated := *e.op
	updated.UpdatedAt = time.Now()
	mutator(&updated)
	r.mu.Unlock()

	// Persist before touching in-memory state.
	if err := r.store.Save(&updated); err != nil {
		return fmt.Errorf("persist operation update: %w", err)
	}

	r.mu.Lock()
	// Re-fetch entry in case it was removed while we were unlocked.
	e, ok = r.ops[id]
	if !ok {
		r.mu.Unlock()
		return nil
	}
	e.op = &updated
	snapshot := updated
	terminal := isTerminal(updated.Status)
	subs := e.subs
	if terminal {
		e.subs = nil
	}
	r.mu.Unlock()

	// Broadcast to subscribers without holding the lock.
	for _, ch := range subs {
		select {
		case ch <- &snapshot:
		default:
			// Slow consumer: drop oldest by draining one and sending the new event.
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- &snapshot:
			default:
			}
		}
		if terminal {
			close(ch)
		}
	}
	return nil
}

// Subscribe returns a buffered channel that receives Operation snapshots on
// every Update call. The channel closes when the operation reaches a terminal
// state or when ctx is done.
//
// If the operation is already in a terminal state a closed channel is returned
// immediately (callers always get the same drain pattern).
//
// Returns ErrOperationNotFound if id does not exist.
func (r *OperationRegistry) Subscribe(ctx context.Context, id string) (<-chan *Operation, error) {
	r.mu.Lock()
	e, ok := r.ops[id]
	if !ok {
		r.mu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrOperationNotFound, id)
	}
	if isTerminal(e.op.Status) {
		r.mu.Unlock()
		ch := make(chan *Operation)
		close(ch)
		return ch, nil
	}
	ch := make(chan *Operation, 16)
	e.subs = append(e.subs, ch)
	r.mu.Unlock()

	// Close the channel when ctx is done (unless already closed by terminal Update).
	go func() {
		<-ctx.Done()
		r.mu.Lock()
		e2, ok := r.ops[id]
		if !ok {
			r.mu.Unlock()
			return
		}
		for i, sub := range e2.subs {
			if sub == ch {
				e2.subs = append(e2.subs[:i], e2.subs[i+1:]...)
				close(ch)
				break
			}
		}
		r.mu.Unlock()
	}()

	return ch, nil
}

// PurgeOlderThan removes terminal operations whose UpdatedAt is older than d
// from both the in-memory registry and the store. Pending/running operations
// are never removed. Returns the number of operations removed.
func (r *OperationRegistry) PurgeOlderThan(d time.Duration) (int, error) {
	cutoff := time.Now().Add(-d)
	r.mu.Lock()
	var toDelete []string
	for id, e := range r.ops {
		if isTerminal(e.op.Status) && e.op.UpdatedAt.Before(cutoff) {
			toDelete = append(toDelete, id)
		}
	}
	for _, id := range toDelete {
		delete(r.ops, id)
	}
	r.mu.Unlock()

	// Delete from store outside the lock; ignore individual delete errors.
	var lastErr error
	for _, id := range toDelete {
		if err := r.store.Delete(id); err != nil {
			lastErr = err
		}
	}
	if lastErr != nil {
		return len(toDelete), fmt.Errorf("purge store: %w", lastErr)
	}
	return len(toDelete), nil
}
