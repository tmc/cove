// cove fleet control plane (paid layer) — see docs/designs/046-fleet-control-plane.md
package fleet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// appendBogus appends a raw JSON entry line to an audit file, bypassing the
// hash chain, to simulate on-disk tampering.
func appendBogus(t *testing.T, path string, e AuditEntry) error {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	return err
}

func TestAuditLogChainIntact(t *testing.T) {
	log, err := NewAuditLog("")
	if err != nil {
		t.Fatalf("NewAuditLog: %v", err)
	}
	if _, err := log.Append("alice", ActionPlace, "team-a/sb-1", AuditAllowed); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := log.Append("bob", ActionStop, "team-a/sb-1", AuditAllowed); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := log.Append("eve", ActionDelete, "team-b/sb-9", AuditDenied); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if idx := log.Verify(); idx != -1 {
		t.Fatalf("Verify on intact chain = %d, want -1", idx)
	}
	entries := log.Entries()
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(entries))
	}
	// First entry has no predecessor; each later entry chains to the prior hash.
	if entries[0].PrevHash != "" {
		t.Errorf("first PrevHash = %q, want empty", entries[0].PrevHash)
	}
	for i := 1; i < len(entries); i++ {
		if entries[i].PrevHash != entries[i-1].Hash {
			t.Errorf("entry %d PrevHash %q != entry %d Hash %q", i, entries[i].PrevHash, i-1, entries[i-1].Hash)
		}
	}
}

func TestAuditLogDetectsEditedMiddleEntry(t *testing.T) {
	log, _ := NewAuditLog("")
	for _, e := range []struct {
		subj   string
		action Action
		res    string
	}{
		{"alice", ActionPlace, "team-a/sb-1"},
		{"bob", ActionStop, "team-a/sb-1"},
		{"carol", ActionDelete, "team-a/sb-1"},
		{"dave", ActionPushPolicy, ""},
	} {
		if _, err := log.Append(e.subj, e.action, e.res, AuditAllowed); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	// Tamper with entry index 1 in place (simulating an attacker editing the log
	// without recomputing the chain). Verify must flag exactly that index.
	log.mu.Lock()
	log.entries[1].Subject = "mallory"
	log.mu.Unlock()

	if idx := log.Verify(); idx != 1 {
		t.Fatalf("Verify after edit = %d, want 1", idx)
	}
}

func TestAuditLogDetectsRewiredHash(t *testing.T) {
	log, _ := NewAuditLog("")
	_, _ = log.Append("alice", ActionPlace, "team-a/sb-1", AuditAllowed)
	_, _ = log.Append("bob", ActionStop, "team-a/sb-1", AuditAllowed)
	_, _ = log.Append("carol", ActionDelete, "team-a/sb-1", AuditAllowed)

	// Break the PrevHash linkage of entry 2 (recompute its own Hash so the
	// per-entry check passes but the chain linkage check catches it).
	log.mu.Lock()
	log.entries[2].PrevHash = "deadbeef"
	rehashed, _ := log.entries[2].computeHash()
	log.entries[2].Hash = rehashed
	log.mu.Unlock()

	if idx := log.Verify(); idx != 2 {
		t.Fatalf("Verify after rewire = %d, want 2", idx)
	}
}

func TestAuditLogPersistAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	at := time.Unix(1_700_000_000, 0)
	log, err := NewAuditLog(path)
	if err != nil {
		t.Fatalf("NewAuditLog: %v", err)
	}
	log.Now = func() time.Time { return at }
	_, _ = log.Append("alice", ActionPlace, "team-a/sb-1", AuditAllowed)
	_, _ = log.Append("bob", ActionStop, "team-a/sb-1", AuditAllowed)

	// Reopen: load must verify the chain and surface the same entries.
	reopened, err := NewAuditLog(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if idx := reopened.Verify(); idx != -1 {
		t.Fatalf("reopened Verify = %d, want -1", idx)
	}
	got := reopened.Entries()
	if len(got) != 2 {
		t.Fatalf("reopened entries = %d, want 2", len(got))
	}
	if got[0].Subject != "alice" || got[1].Subject != "bob" {
		t.Errorf("reopened subjects = %q,%q", got[0].Subject, got[1].Subject)
	}
	// Append after reload continues the chain.
	if _, err := reopened.Append("carol", ActionDelete, "team-a/sb-1", AuditAllowed); err != nil {
		t.Fatalf("append after reload: %v", err)
	}
	if idx := reopened.Verify(); idx != -1 {
		t.Fatalf("Verify after append = %d, want -1", idx)
	}
}

func TestNewAuditLogRejectsTamperedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	log, _ := NewAuditLog(path)
	_, _ = log.Append("alice", ActionPlace, "team-a/sb-1", AuditAllowed)
	_, _ = log.Append("bob", ActionStop, "team-a/sb-1", AuditAllowed)

	// Corrupt the persisted file by appending an entry with a bogus hash chain.
	bogus := AuditEntry{TS: 1, Subject: "mallory", Action: ActionDelete, Resource: "x", Result: AuditAllowed, PrevHash: "nope", Hash: "nope"}
	if err := appendBogus(t, path, bogus); err != nil {
		t.Fatalf("appendBogus: %v", err)
	}
	if _, err := NewAuditLog(path); err == nil {
		t.Fatal("NewAuditLog should reject a tampered persisted chain")
	}
}
