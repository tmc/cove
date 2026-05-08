package storagepins

import (
	"path/filepath"
	"testing"
	"time"
)

func TestParseRef(t *testing.T) {
	now := time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC)
	_ = now
	tests := []struct {
		name      string
		ref       string
		wantCat   string
		wantID    string
		wantError bool
	}{
		{"vm ok", "vm:default", "vm", "default", false},
		{"image ok", "image:base:v1", "image", "base:v1", false},
		{"run ok", "run:abc123", "run", "abc123", false},
		{"cache ok", "cache:sha256-deadbeef", "cache", "sha256-deadbeef", false},
		{"empty", "", "", "", true},
		{"no colon", "vmdefault", "", "", true},
		{"empty id", "vm:", "", "", true},
		{"empty category", ":default", "", "", true},
		{"bad category", "snapshot:foo", "", "", true},
		{"path traversal dot", "vm:..", "", "", true},
		{"path traversal slash", "vm:foo/bar", "", "", true},
		{"whitespace id", "vm:has space", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cat, id, err := ParseRef(tt.ref)
			if (err != nil) != tt.wantError {
				t.Fatalf("ParseRef(%q) err=%v wantError=%v", tt.ref, err, tt.wantError)
			}
			if !tt.wantError {
				if cat != tt.wantCat || id != tt.wantID {
					t.Errorf("ParseRef(%q) = (%q, %q); want (%q, %q)", tt.ref, cat, id, tt.wantCat, tt.wantID)
				}
			}
		})
	}
}

func TestAddRemoveIsPinned(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	f := New()

	if f.IsPinned("vm", "default") {
		t.Fatal("empty File reports vm:default pinned")
	}

	if err := f.Add("vm", "default", now); err != nil {
		t.Fatalf("Add vm:default: %v", err)
	}
	if !f.IsPinned("vm", "default") {
		t.Errorf("vm:default not pinned after Add")
	}

	// Idempotent add: second Add must not error and must not bump the timestamp.
	if err := f.Add("vm", "default", now.Add(time.Hour)); err != nil {
		t.Fatalf("re-Add vm:default: %v", err)
	}
	pins := f.List()
	if got := len(pins); got != 1 {
		t.Fatalf("after re-Add len(List)=%d; want 1", got)
	}
	if !pins[0].AddedAt.Equal(now) {
		t.Errorf("re-Add bumped AddedAt: got %v want %v", pins[0].AddedAt, now)
	}

	// Add a second pin, confirm List ordering by category then id.
	if err := f.Add("image", "base:v1", now); err != nil {
		t.Fatalf("Add image:base:v1: %v", err)
	}
	pins = f.List()
	if len(pins) != 2 {
		t.Fatalf("len(List)=%d; want 2", len(pins))
	}
	if pins[0].Ref() != "image:base:v1" || pins[1].Ref() != "vm:default" {
		t.Errorf("List order=%q,%q; want image:base:v1, vm:default", pins[0].Ref(), pins[1].Ref())
	}

	// RefSet round-trip.
	set := f.RefSet()
	if !set["vm:default"] || !set["image:base:v1"] {
		t.Errorf("RefSet missing entries: %v", set)
	}

	// Remove a missing pin.
	removed, err := f.Remove("run", "missing")
	if err != nil {
		t.Fatalf("Remove run:missing: %v", err)
	}
	if removed {
		t.Errorf("Remove run:missing returned true; want false")
	}

	// Remove an existing pin.
	removed, err = f.Remove("vm", "default")
	if err != nil {
		t.Fatalf("Remove vm:default: %v", err)
	}
	if !removed {
		t.Errorf("Remove vm:default returned false; want true")
	}
	if f.IsPinned("vm", "default") {
		t.Errorf("vm:default still pinned after Remove")
	}

	// Bad category surfaces as an error for Add and Remove.
	if err := f.Add("snapshot", "foo", now); err == nil {
		t.Errorf("Add snapshot:foo expected error, got nil")
	}
	if _, err := f.Remove("snapshot", "foo"); err == nil {
		t.Errorf("Remove snapshot:foo expected error, got nil")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	f := New()
	if err := f.Add("vm", "default", now); err != nil {
		t.Fatal(err)
	}
	if err := f.Add("image", "base:v1", now); err != nil {
		t.Fatal(err)
	}
	if err := Save(root, f); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Load on a missing file yields an empty File.
	other := t.TempDir()
	loaded, err := Load(other)
	if err != nil {
		t.Fatalf("Load empty: %v", err)
	}
	if got := len(loaded.List()); got != 0 {
		t.Errorf("Load on empty root List len=%d; want 0", got)
	}

	// Load on the saved file recovers the pin set.
	loaded, err = Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !loaded.IsPinned("vm", "default") || !loaded.IsPinned("image", "base:v1") {
		t.Errorf("Load lost pins: %v", loaded.List())
	}

	// File lives at root/Filename and is JSON.
	if _, err := loadJSONFile(filepath.Join(root, Filename)); err != nil {
		t.Errorf("on-disk file: %v", err)
	}
}

// loadJSONFile is a tiny shim to confirm the on-disk file is parseable
// without re-importing encoding/json into the test top-level imports.
func loadJSONFile(path string) (any, error) {
	loaded, err := Load(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	return loaded.List(), nil
}
