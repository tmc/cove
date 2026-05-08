package storagepins

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAddRejectsBadCategoryAndEmptyID(t *testing.T) {
	now := time.Now()
	f := New()
	if err := f.Add("snapshot", "x", now); err == nil {
		t.Fatal("Add(bad category) = nil, want error")
	}
	if err := f.Add("vm", "", now); err == nil {
		t.Fatal("Add(empty id) = nil, want error")
	}
}

func TestRemoveRejectsBadCategory(t *testing.T) {
	f := New()
	if _, err := f.Remove("snapshot", "x"); err == nil {
		t.Fatal("Remove(bad category) = nil, want error")
	}
}

func TestRemoveMissingReturnsFalse(t *testing.T) {
	ok, err := New().Remove("vm", "ghost")
	if err != nil || ok {
		t.Fatalf("Remove(missing) = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestParseRefSingleDotID(t *testing.T) {
	if _, _, err := ParseRef("vm:."); err == nil {
		t.Fatal("ParseRef(vm:.) = nil, want error")
	}
}

func TestLoadMalformedJSON(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, Filename), []byte("{bogus"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(root); err == nil {
		t.Fatal("Load(malformed) = nil, want error")
	}
}

func TestLoadDropsBadOnDiskRecords(t *testing.T) {
	root := t.TempDir()
	data := []byte(`{"pins":[
        {"category":"vm","id":"keep","added_at":"2026-01-01T00:00:00Z"},
        {"category":"snapshot","id":"junk","added_at":"2026-01-01T00:00:00Z"},
        {"category":"vm","id":"","added_at":"2026-01-01T00:00:00Z"}
    ]}`)
	if err := os.WriteFile(filepath.Join(root, Filename), data, 0644); err != nil {
		t.Fatal(err)
	}
	f, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	pins := f.List()
	if len(pins) != 1 || pins[0].Category != "vm" || pins[0].ID != "keep" {
		t.Fatalf("Load() pins = %#v, want only vm:keep", pins)
	}
}

func TestAddDuplicatePreservesAddedAt(t *testing.T) {
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	f := New()
	for _, ts := range []time.Time{t1, t2} {
		if err := f.Add("vm", "x", ts); err != nil {
			t.Fatal(err)
		}
	}
	pins := f.List()
	if len(pins) != 1 || !pins[0].AddedAt.Equal(t1.UTC()) {
		t.Fatalf("List() = %#v, want one pin with AddedAt=%v", pins, t1)
	}
}
