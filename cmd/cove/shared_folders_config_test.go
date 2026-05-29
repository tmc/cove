package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSanitizeSharedFolderTag(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "share"},
		{"whitespace", "   ", "share"},
		{"simple", "data", "data"},
		{"uppercase", "MyShare", "myshare"},
		{"path-with-slashes", "/Users/me/code", "users-me-code"},
		{"special-chars", "foo!@#bar", "foo-bar"},
		{"trim-dashes", "---x---", "x"},
		{"only-special", "!!!", "share"},
		{"digits", "vol123", "vol123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeSharedFolderTag(tt.in)
			if got != tt.want {
				t.Errorf("sanitizeSharedFolderTag(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestUniqueTag(t *testing.T) {
	existing := []SharedFolderEntry{{Tag: "data"}, {Tag: "data-2"}}
	if got := uniqueTag("fresh", existing); got != "fresh" {
		t.Errorf("uniqueTag(fresh) = %q, want fresh", got)
	}
	if got := uniqueTag("data", existing); got != "data-3" {
		t.Errorf("uniqueTag(data) = %q, want data-3", got)
	}
	// Sanitization happens before uniqueness.
	if got := uniqueTag("DATA", existing); got != "data-3" {
		t.Errorf("uniqueTag(DATA) = %q, want data-3", got)
	}
}

func TestLoadSaveSharedFolders(t *testing.T) {
	dir := t.TempDir()
	// Empty when no file.
	if got := LoadSharedFolders(dir); got != nil {
		t.Errorf("LoadSharedFolders(empty dir) = %v, want nil", got)
	}
	// Round-trip.
	want := []SharedFolderEntry{
		{Path: "/a", Tag: "a", ReadOnly: false},
		{Path: "/b", Tag: "b", ReadOnly: true},
	}
	if err := saveSharedFolders(dir, want); err != nil {
		t.Fatalf("saveSharedFolders: %v", err)
	}
	got := LoadSharedFolders(dir)
	if len(got) != len(want) {
		t.Fatalf("LoadSharedFolders len = %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("entry[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
	// Save empty removes file.
	if err := saveSharedFolders(dir, nil); err != nil {
		t.Fatalf("saveSharedFolders(nil): %v", err)
	}
	if got := LoadSharedFolders(dir); got != nil {
		t.Errorf("LoadSharedFolders after clear = %v, want nil", got)
	}
	// Removing again is a no-op.
	if err := saveSharedFolders(dir, nil); err != nil {
		t.Fatalf("saveSharedFolders(nil) repeat: %v", err)
	}
	// Corrupt file -> nil.
	cfg := filepath.Join(dir, "shared_folders.json")
	if err := os.WriteFile(cfg, []byte("{not json"), 0644); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
	if got := LoadSharedFolders(dir); got != nil {
		t.Errorf("LoadSharedFolders(corrupt) = %v, want nil", got)
	}
}
