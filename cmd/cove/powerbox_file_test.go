//go:build darwin

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/apple/foundation"
)

func TestPowerboxFileExtensionAllowed(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		extensions []string
		want       bool
	}{
		{name: "empty extensions allow", path: "anything.txt", want: true},
		{name: "iso", path: "installer.iso", extensions: []string{".iso", ".ipsw"}, want: true},
		{name: "ipsw upper", path: "Restore.IPSW", extensions: []string{"iso", "ipsw"}, want: true},
		{name: "reject", path: "notes.txt", extensions: []string{"iso", "ipsw"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := powerboxFileExtensionAllowed(tt.path, tt.extensions)
			if got != tt.want {
				t.Fatalf("powerboxFileExtensionAllowed(%q, %v) = %v, want %v", tt.path, tt.extensions, got, tt.want)
			}
		})
	}
}

func TestPowerboxAllowedContentTypes(t *testing.T) {
	got := powerboxAllowedContentTypes([]string{".iso", "ipsw", "", "  .iso  "})
	if len(got) == 0 {
		t.Fatal("powerboxAllowedContentTypes returned no UTTypes")
	}
	for i, typ := range got {
		if typ.GetID() == 0 {
			t.Fatalf("type %d has nil ID", i)
		}
	}
}

func TestPowerboxFileGrantForURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "installer.iso")
	if err := os.WriteFile(path, []byte("iso"), 0600); err != nil {
		t.Fatalf("write ISO: %v", err)
	}
	url := foundation.NewURLFileURLWithPathIsDirectory(path, false)
	grant, err := powerboxFileGrantForURL(url, []string{"iso", "ipsw"})
	if err != nil {
		if securityScopedBookmarkUnavailable(err) {
			t.Skipf("security-scoped bookmarks unavailable in this process: %v", err)
		}
		t.Fatalf("powerboxFileGrantForURL: %v", err)
	}
	if grant.Path != path {
		t.Fatalf("grant path = %q, want %q", grant.Path, path)
	}
	if len(grant.Bookmark) == 0 {
		t.Fatal("grant bookmark is empty")
	}
}

func TestPowerboxFileGrantForURLRejectsExtension(t *testing.T) {
	path := filepath.Join(t.TempDir(), "installer.txt")
	if err := os.WriteFile(path, []byte("not iso"), 0600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	url := foundation.NewURLFileURLWithPathIsDirectory(path, false)
	if _, err := powerboxFileGrantForURL(url, []string{"iso", "ipsw"}); err == nil {
		t.Fatal("powerboxFileGrantForURL succeeded for unsupported extension")
	}
}
