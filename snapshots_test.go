package main

import "testing"

func TestValidateSnapshotName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"checkpoint1", false},
		{"my-snapshot", false},
		{"v2.0", false},
		{"", true},          // empty
		{"a/b", true},       // forward slash
		{"a\\b", true},      // backslash
		{".", true},          // current dir
		{"..", true},         // parent dir
		{".hidden", false},   // dotfile is fine
		{"has spaces", false}, // spaces are ok
	}
	for _, tt := range tests {
		err := validateSnapshotName(tt.name)
		if (err != nil) != tt.wantErr {
			t.Errorf("validateSnapshotName(%q) error = %v, wantErr %v", tt.name, err, tt.wantErr)
		}
	}
}

func TestDiskSnapshotSaveRejectsBadNames(t *testing.T) {
	dir := t.TempDir()
	mgr := NewDiskSnapshotManager(dir)

	for _, name := range []string{"", "a/b", "..", "."} {
		if err := mgr.Save(name, DiskSnapshotSystem, ""); err == nil {
			t.Errorf("Save(%q) should have failed", name)
		}
	}
}

func TestDiskSnapshotSaveAndList(t *testing.T) {
	dir := t.TempDir()
	mgr := NewDiskSnapshotManager(dir)

	// Save should succeed even without a disk.img (it warns but creates metadata).
	if err := mgr.Save("test-snap", DiskSnapshotSystem, "test description"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	snaps, err := mgr.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("got %d snapshots, want 1", len(snaps))
	}
	if snaps[0].Name != "test-snap" {
		t.Errorf("name = %q, want %q", snaps[0].Name, "test-snap")
	}
	if snaps[0].Description != "test description" {
		t.Errorf("description = %q, want %q", snaps[0].Description, "test description")
	}
}

func TestDiskSnapshotDeleteRejectsBadNames(t *testing.T) {
	dir := t.TempDir()
	mgr := NewDiskSnapshotManager(dir)

	if err := mgr.Delete(""); err == nil {
		t.Error("Delete('') should have failed")
	}
	if err := mgr.Delete("../escape"); err == nil {
		t.Error("Delete('../escape') should have failed")
	}
}
