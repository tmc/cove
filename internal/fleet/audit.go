// cove fleet control plane (paid layer) — see docs/designs/046-fleet-control-plane.md
package fleet

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// AuditResult is the outcome recorded for an audited action.
type AuditResult string

// The two outcomes: an action either succeeded or was denied/failed.
const (
	AuditAllowed AuditResult = "allowed"
	AuditDenied  AuditResult = "denied"
)

// AuditEntry is one record in the tamper-evident who-did-what log. Hash chains
// each entry to its predecessor: Hash = sha256(PrevHash + canonical(entry)),
// where canonical(entry) is the entry's JSON with Hash zeroed. Any edit to a
// past entry breaks the chain at that index, which Verify detects.
type AuditEntry struct {
	// TS is the event time in Unix nanoseconds.
	TS int64 `json:"ts"`
	// Subject is the authenticated identity that took the action.
	Subject string `json:"subject"`
	// Action is the guarded operation (place/stop/delete/push-policy/...).
	Action Action `json:"action"`
	// Resource is the target: "namespace/id", "namespace", or "" for fleet-wide.
	Resource string `json:"resource"`
	// Result is allowed or denied.
	Result AuditResult `json:"result"`
	// PrevHash is the hash of the previous entry (empty for the first entry).
	PrevHash string `json:"prev_hash"`
	// Hash is sha256(PrevHash + canonical(entry)).
	Hash string `json:"hash"`
}

// canonicalBytes returns the entry's canonical JSON with Hash zeroed, used as
// the hash input. Marshaling a struct with fixed field order is deterministic.
func (e AuditEntry) canonicalBytes() ([]byte, error) {
	c := e
	c.Hash = ""
	return json.Marshal(c)
}

// computeHash returns sha256(PrevHash + canonical(entry)) as lowercase hex.
func (e AuditEntry) computeHash() (string, error) {
	body, err := e.canonicalBytes()
	if err != nil {
		return "", fmt.Errorf("canonicalize audit entry: %w", err)
	}
	h := sha256.New()
	h.Write([]byte(e.PrevHash))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// AuditLog is an append-only, hash-chained who-did-what log. Entries are held in
// memory and (when a path is configured) appended to a JSON-lines file. It is
// safe for concurrent use. The zero value is not usable; build one with
// NewAuditLog.
type AuditLog struct {
	// Now is injected for testability; nil falls back to time.Now.
	Now func() time.Time

	mu      sync.Mutex
	path    string
	entries []AuditEntry
}

// NewAuditLog opens (or creates) the log backed by path. An empty path keeps the
// log entirely in memory. On open it loads and verifies any existing chain,
// returning an error if the persisted log is already tampered.
func NewAuditLog(path string) (*AuditLog, error) {
	l := &AuditLog{path: path}
	if path != "" {
		if err := l.load(); err != nil {
			return nil, err
		}
	}
	return l, nil
}

func (l *AuditLog) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *AuditLog) load() error {
	data, err := os.ReadFile(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read audit log: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	for dec.More() {
		var e AuditEntry
		if err := dec.Decode(&e); err != nil {
			return fmt.Errorf("parse audit log: %w", err)
		}
		l.entries = append(l.entries, e)
	}
	if idx := verifyChain(l.entries); idx >= 0 {
		return fmt.Errorf("audit log tampered at entry %d", idx)
	}
	return nil
}

// Append records an authorized (or denied) action, linking it to the chain. It
// returns the stored entry including its computed hash.
func (l *AuditLog) Append(subject string, action Action, resource string, result AuditResult) (AuditEntry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	prev := ""
	if n := len(l.entries); n > 0 {
		prev = l.entries[n-1].Hash
	}
	e := AuditEntry{
		TS:       l.now().UnixNano(),
		Subject:  subject,
		Action:   action,
		Resource: resource,
		Result:   result,
		PrevHash: prev,
	}
	hash, err := e.computeHash()
	if err != nil {
		return AuditEntry{}, err
	}
	e.Hash = hash
	l.entries = append(l.entries, e)
	if err := l.appendFile(e); err != nil {
		// Roll back the in-memory append so memory and disk stay consistent.
		l.entries = l.entries[:len(l.entries)-1]
		return AuditEntry{}, err
	}
	return e, nil
}

// appendFile appends one entry as a JSON line. The caller holds l.mu.
func (l *AuditLog) appendFile(e AuditEntry) error {
	if l.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0700); err != nil {
		return fmt.Errorf("create audit log dir: %w", err)
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("open audit log: %w", err)
	}
	defer f.Close()
	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshal audit entry: %w", err)
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("write audit entry: %w", err)
	}
	return nil
}

// Entries returns a copy of the chain in append order.
func (l *AuditLog) Entries() []AuditEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]AuditEntry(nil), l.entries...)
}

// Verify walks the chain and returns the index of the first entry whose stored
// hash does not match its recomputed value or whose PrevHash does not chain to
// its predecessor. It returns -1 when the chain is intact.
func (l *AuditLog) Verify() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return verifyChain(l.entries)
}

// verifyChain returns the first tampered index, or -1 if intact. It is the pure
// core of Verify, also used to validate a freshly loaded log.
func verifyChain(entries []AuditEntry) int {
	prev := ""
	for i, e := range entries {
		if e.PrevHash != prev {
			return i
		}
		want, err := e.computeHash()
		if err != nil || want != e.Hash {
			return i
		}
		prev = e.Hash
	}
	return -1
}
