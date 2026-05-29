//go:build darwin

package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestSecurityBookmarkStoreReadWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bookmarks.json")
	store := securityBookmarkStore{
		Version: 1,
		Entries: map[string]securityBookmarkEntry{
			"vm:test": {
				Key:      "vm:test",
				Kind:     "vm-root",
				Path:     "/tmp/vm",
				Bookmark: base64.StdEncoding.EncodeToString([]byte("bookmark-bytes")),
				Updated:  "2026-05-27T00:00:00Z",
			},
		},
	}
	if err := writeSecurityBookmarkStore(path, store); err != nil {
		t.Fatalf("writeSecurityBookmarkStore: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat bookmark store: %v", err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("bookmark store mode = %v, want 0600", got)
	}
	got, err := readSecurityBookmarkStore(path)
	if err != nil {
		t.Fatalf("readSecurityBookmarkStore: %v", err)
	}
	entry := got.Entries["vm:test"]
	if entry.Bookmark != store.Entries["vm:test"].Bookmark {
		t.Fatalf("bookmark was not preserved")
	}
	report := securityBookmarkEntryForReport(entry)
	if report.BookmarkSize != len("bookmark-bytes") {
		t.Fatalf("report bookmark size = %d, want %d", report.BookmarkSize, len("bookmark-bytes"))
	}
}
