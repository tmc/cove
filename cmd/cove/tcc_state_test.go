package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadTCCStateMissingFileReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tcc.json")
	s, err := LoadTCCState(path)
	if err != nil {
		t.Fatalf("LoadTCCState on missing file: %v", err)
	}
	if s == nil {
		t.Fatal("LoadTCCState returned nil state")
	}
	if s.Version != TCCStateVersion {
		t.Errorf("Version = %d, want %d", s.Version, TCCStateVersion)
	}
	if len(s.Host) != 0 {
		t.Errorf("Host = %v, want empty", s.Host)
	}
}

func TestSaveLoadTCCStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "tcc.json")
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	s := &TCCState{}
	s.SetHostEntry("system_events", TCCResultGranted, now)
	s.SetHostEntry("utm", TCCResultSkipped, now)
	if err := SaveTCCState(path, s); err != nil {
		t.Fatalf("SaveTCCState: %v", err)
	}
	loaded, err := LoadTCCState(path)
	if err != nil {
		t.Fatalf("LoadTCCState: %v", err)
	}
	if loaded.Version != TCCStateVersion {
		t.Errorf("Version = %d, want %d", loaded.Version, TCCStateVersion)
	}
	se, ok := loaded.HostEntry("system_events")
	if !ok || se.Result != TCCResultGranted {
		t.Errorf("HostEntry(system_events) = (%v, %v), want granted", se, ok)
	}
	utm, ok := loaded.HostEntry("utm")
	if !ok || utm.Result != TCCResultSkipped {
		t.Errorf("HostEntry(utm) = (%v, %v), want skipped", utm, ok)
	}
	if !se.PreflightedAt.Equal(now) {
		t.Errorf("PreflightedAt = %v, want %v", se.PreflightedAt, now)
	}
}

func TestSaveTCCStateNilFails(t *testing.T) {
	if err := SaveTCCState(filepath.Join(t.TempDir(), "tcc.json"), nil); err == nil {
		t.Fatal("SaveTCCState(nil) returned no error")
	}
}

func TestSaveTCCStateAtomicRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tcc.json")
	now := time.Now()
	s := &TCCState{}
	s.SetHostEntry("system_events", TCCResultGranted, now)
	if err := SaveTCCState(path, s); err != nil {
		t.Fatalf("SaveTCCState: %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("expected no leftover .tmp file, stat err = %v", err)
	}
}

func TestHostEntryNilSafe(t *testing.T) {
	var s *TCCState
	if _, ok := s.HostEntry("anything"); ok {
		t.Error("nil state HostEntry returned ok=true")
	}
	empty := &TCCState{}
	if _, ok := empty.HostEntry("anything"); ok {
		t.Error("empty state HostEntry returned ok=true")
	}
}

func TestLoadTCCStateMalformedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tcc.json")
	if err := os.WriteFile(path, []byte("{not json"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTCCState(path); err == nil {
		t.Error("LoadTCCState on malformed file returned no error")
	}
}
