package vmconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSharedFoldersSaveLoadAndRemove(t *testing.T) {
	dir := t.TempDir()
	folders := []SharedFolderEntry{
		{Path: "/tmp/share", Tag: "share", ReadOnly: true},
		{Path: "/Users/me/work", Tag: "work"},
	}
	if err := SaveSharedFolders(dir, folders); err != nil {
		t.Fatalf("SaveSharedFolders() error = %v", err)
	}
	if got := LoadSharedFolders(dir); !reflect.DeepEqual(got, folders) {
		t.Fatalf("LoadSharedFolders() = %#v, want %#v", got, folders)
	}
	if err := SaveSharedFolders(dir, nil); err != nil {
		t.Fatalf("SaveSharedFolders(nil) error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "shared_folders.json")); !os.IsNotExist(err) {
		t.Fatalf("shared_folders.json still exists: %v", err)
	}
	if got := LoadSharedFolders(dir); got != nil {
		t.Fatalf("LoadSharedFolders() after remove = %#v, want nil", got)
	}
}

func TestLoadSharedFoldersIgnoresMissingAndMalformed(t *testing.T) {
	dir := t.TempDir()
	if got := LoadSharedFolders(dir); got != nil {
		t.Fatalf("LoadSharedFolders(missing) = %#v, want nil", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "shared_folders.json"), []byte("{"), 0644); err != nil {
		t.Fatalf("WriteFile(shared_folders.json) error = %v", err)
	}
	if got := LoadSharedFolders(dir); got != nil {
		t.Fatalf("LoadSharedFolders(malformed) = %#v, want nil", got)
	}
}

func TestSanitizeSharedFolderTag(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: "share"},
		{name: "spaces", in: "   ", want: "share"},
		{name: "letters and digits", in: "Work123", want: "work123"},
		{name: "punctuation collapsed", in: " My Work:/Project ", want: "my-work-project"},
		{name: "only punctuation", in: " --// ", want: "share"},
		{name: "repeated separators", in: "a---b___c", want: "a-b-c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SanitizeSharedFolderTag(tt.in); got != tt.want {
				t.Fatalf("SanitizeSharedFolderTag(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestUniqueSharedFolderTag(t *testing.T) {
	existing := []SharedFolderEntry{
		{Tag: "share"},
		{Tag: "share-2"},
		{Tag: "work"},
	}
	tests := []struct {
		name string
		base string
		want string
	}{
		{name: "unused", base: "logs", want: "logs"},
		{name: "sanitized unused", base: "Build Artifacts", want: "build-artifacts"},
		{name: "default collision", base: "", want: "share-3"},
		{name: "named collision", base: "work", want: "work-2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := UniqueSharedFolderTag(tt.base, existing); got != tt.want {
				t.Fatalf("UniqueSharedFolderTag(%q) = %q, want %q", tt.base, got, tt.want)
			}
		})
	}
}
