package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPITSnapshotManagerSaveErrors(t *testing.T) {
	tests := []struct {
		name    string
		snap    string
		hooks   PITSaveHooks
		wantSub string
	}{
		{
			name:    "invalid name",
			snap:    "bad/name",
			hooks:   PITSaveHooks{SaveState: func(string) error { return nil }, CloneDisk: func(string) (int64, error) { return 0, nil }},
			wantSub: "snapshot name",
		},
		{
			name:    "missing save-state hook",
			snap:    "snap1",
			hooks:   PITSaveHooks{CloneDisk: func(string) (int64, error) { return 0, nil }},
			wantSub: "save state hook",
		},
		{
			name:    "missing clone-disk hook",
			snap:    "snap1",
			hooks:   PITSaveHooks{SaveState: func(string) error { return nil }},
			wantSub: "clone disk hook",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewPITSnapshotManager(t.TempDir())
			err := m.Save(tt.snap, tt.hooks)
			if err == nil || !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("err = %v, want substring %q", err, tt.wantSub)
			}
		})
	}
}

func TestPITSnapshotManagerSaveRejectsExisting(t *testing.T) {
	dir := t.TempDir()
	m := NewPITSnapshotManager(dir)
	// Pre-create the snapshot dir so Save sees it as already-exists.
	if err := os.MkdirAll(filepath.Join(dir, "pit", "snap1"), 0755); err != nil {
		t.Fatal(err)
	}
	hooks := PITSaveHooks{
		Now:       func() time.Time { return time.Unix(0, 0) },
		SaveState: func(string) error { return nil },
		CloneDisk: func(string) (int64, error) { return 0, nil },
	}
	err := m.Save("snap1", hooks)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("err = %v, want already-exists", err)
	}
}

func TestPITSnapshotManagerLoadErrors(t *testing.T) {
	dir := t.TempDir()
	m := NewPITSnapshotManager(dir)

	if _, err := m.Load("bad/name"); err == nil {
		t.Errorf("invalid name: want error")
	}

	_, err := m.Load("missing")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing manifest: err = %v, want not-found", err)
	}

	// Bad JSON triggers parse error.
	snapDir := filepath.Join(dir, "pit", "broken")
	if err := os.MkdirAll(snapDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapDir, "manifest.json"), []byte("{not-json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Load("broken"); err == nil || !strings.Contains(err.Error(), "parse") {
		t.Errorf("bad json: err = %v, want parse error", err)
	}
}

func TestPITSnapshotManagerLoadFillsDefaults(t *testing.T) {
	dir := t.TempDir()
	m := NewPITSnapshotManager(dir)
	snapDir := filepath.Join(dir, "pit", "snap1")
	if err := os.MkdirAll(snapDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Manifest with empty Name/FilePath/DiskFileName/DiskPath/VMStatePath —
	// Load should fill each from the manager's path conventions.
	if err := os.WriteFile(filepath.Join(snapDir, "manifest.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	info, err := m.Load("snap1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if info.Name != "snap1" {
		t.Errorf("Name = %q, want snap1", info.Name)
	}
	if info.FilePath != snapDir {
		t.Errorf("FilePath = %q, want %q", info.FilePath, snapDir)
	}
	if info.DiskFileName == "" {
		t.Errorf("DiskFileName empty")
	}
	if info.DiskPath == "" {
		t.Errorf("DiskPath empty")
	}
	if info.VMStatePath == "" {
		t.Errorf("VMStatePath empty")
	}
}
