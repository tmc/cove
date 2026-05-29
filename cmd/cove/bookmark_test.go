//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecurityScopedBookmarkRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "grant.txt")
	if err := os.WriteFile(path, []byte("cove security-scoped bookmark proof\n"), 0600); err != nil {
		t.Fatalf("write bookmark fixture: %v", err)
	}

	report, err := securityScopedBookmarkRoundTrip(path)
	if err != nil {
		if securityScopedBookmarkUnavailable(err) {
			t.Skipf("security-scoped bookmarks unavailable in this process: %v", err)
		}
		t.Fatalf("securityScopedBookmarkRoundTrip: %v", err)
	}
	if report.BookmarkSize == 0 {
		t.Fatalf("bookmark size = 0")
	}
	if report.ResolvedPath != report.Path {
		t.Fatalf("resolved path = %q, want %q", report.ResolvedPath, report.Path)
	}
	if !report.Started {
		t.Fatalf("started access = false")
	}
	if report.ReadBytes == 0 || report.SHA256 == "" {
		t.Fatalf("read proof incomplete: %+v", report)
	}
}

func securityScopedBookmarkUnavailable(err error) bool {
	s := err.Error()
	for _, phrase := range []string{
		"not permitted",
		"permission",
		"Operation not permitted",
		"com.apple.security.files.bookmarks.app-scope",
		"entitlement",
	} {
		if strings.Contains(s, phrase) {
			return true
		}
	}
	return false
}
