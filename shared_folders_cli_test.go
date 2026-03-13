package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAddSharedFolderEntry(t *testing.T) {
	vmDir := t.TempDir()
	hostDir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(hostDir, 0755); err != nil {
		t.Fatalf("mkdir host dir: %v", err)
	}

	entry, added, err := addSharedFolderEntry(vmDir, hostDir, "", false)
	if err != nil {
		t.Fatalf("addSharedFolderEntry() error = %v", err)
	}
	if !added {
		t.Fatalf("expected added=true")
	}
	wantPath := resolvePath(hostDir)
	if entry.Path != wantPath {
		t.Fatalf("path = %q, want %q", entry.Path, wantPath)
	}
	if entry.Tag != "data" {
		t.Fatalf("tag = %q, want %q", entry.Tag, "data")
	}

	folders := LoadSharedFolders(vmDir)
	if len(folders) != 1 {
		t.Fatalf("len(folders) = %d, want 1", len(folders))
	}
}

func TestAddSharedFolderEntryDuplicatePath(t *testing.T) {
	vmDir := t.TempDir()
	hostDir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(hostDir, 0755); err != nil {
		t.Fatalf("mkdir host dir: %v", err)
	}

	if _, _, err := addSharedFolderEntry(vmDir, hostDir, "data", false); err != nil {
		t.Fatalf("first add error = %v", err)
	}
	entry, added, err := addSharedFolderEntry(vmDir, hostDir, "other", true)
	if err != nil {
		t.Fatalf("second add error = %v", err)
	}
	if added {
		t.Fatalf("expected added=false for duplicate path")
	}
	if entry.Tag != "data" {
		t.Fatalf("duplicate should keep original tag, got %q", entry.Tag)
	}

	folders := LoadSharedFolders(vmDir)
	if len(folders) != 1 {
		t.Fatalf("len(folders) = %d, want 1", len(folders))
	}
}

func TestAddSharedFolderEntryTagCollision(t *testing.T) {
	vmDir := t.TempDir()
	hostA := filepath.Join(t.TempDir(), "a")
	hostB := filepath.Join(t.TempDir(), "b")
	if err := os.MkdirAll(hostA, 0755); err != nil {
		t.Fatalf("mkdir hostA: %v", err)
	}
	if err := os.MkdirAll(hostB, 0755); err != nil {
		t.Fatalf("mkdir hostB: %v", err)
	}

	if _, _, err := addSharedFolderEntry(vmDir, hostA, "shared", false); err != nil {
		t.Fatalf("first add error = %v", err)
	}
	if _, _, err := addSharedFolderEntry(vmDir, hostB, "shared", false); err == nil {
		t.Fatalf("expected tag collision error")
	}
}

func TestRemoveSharedFolderEntry(t *testing.T) {
	vmDir := t.TempDir()
	hostA := filepath.Join(t.TempDir(), "a")
	hostB := filepath.Join(t.TempDir(), "b")
	if err := os.MkdirAll(hostA, 0755); err != nil {
		t.Fatalf("mkdir hostA: %v", err)
	}
	if err := os.MkdirAll(hostB, 0755); err != nil {
		t.Fatalf("mkdir hostB: %v", err)
	}

	if _, _, err := addSharedFolderEntry(vmDir, hostA, "one", false); err != nil {
		t.Fatalf("add hostA: %v", err)
	}
	if _, _, err := addSharedFolderEntry(vmDir, hostB, "two", false); err != nil {
		t.Fatalf("add hostB: %v", err)
	}

	removed, err := removeSharedFolderEntry(vmDir, "one")
	if err != nil {
		t.Fatalf("remove by tag: %v", err)
	}
	if !removed {
		t.Fatalf("expected removed=true")
	}

	folders := LoadSharedFolders(vmDir)
	if len(folders) != 1 {
		t.Fatalf("len(folders) = %d, want 1", len(folders))
	}
	if folders[0].Tag != "two" {
		t.Fatalf("remaining tag = %q, want %q", folders[0].Tag, "two")
	}
}

func TestMountContainsAllTags(t *testing.T) {
	listing := "ml-explore\nmlx-go\ntmc\n"
	if !mountContainsAllTags(listing, []string{"ml-explore", "tmc"}) {
		t.Fatalf("expected tags to be found")
	}
	if mountContainsAllTags(listing, []string{"missing"}) {
		t.Fatalf("unexpected success for missing tag")
	}
	if !mountContainsAllTags(listing, nil) {
		t.Fatalf("empty tag set should always pass")
	}
}
