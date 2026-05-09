package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultTCCStatePath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	got, err := DefaultTCCStatePath()
	if err != nil {
		t.Fatalf("DefaultTCCStatePath: %v", err)
	}
	want := filepath.Join(home, ".vz", "runtime", "tcc.json")
	if got != want {
		t.Fatalf("DefaultTCCStatePath = %q, want %q", got, want)
	}
}

func TestLoadTCCStateReadErrorOnDirectory(t *testing.T) {
	dir := t.TempDir()
	// pointing LoadTCCState at a directory triggers a non-IsNotExist read error.
	_, err := LoadTCCState(dir)
	if err == nil {
		t.Fatal("LoadTCCState(dir) = nil, want read error")
	}
	if !strings.Contains(err.Error(), "read tcc state") {
		t.Fatalf("err = %q, want substring 'read tcc state'", err.Error())
	}
}

func TestLoadTCCStateRestoresMissingVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tcc.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadTCCState(path)
	if err != nil {
		t.Fatalf("LoadTCCState: %v", err)
	}
	if got.Version != TCCStateVersion {
		t.Fatalf("Version = %d, want %d", got.Version, TCCStateVersion)
	}
}

func TestSaveTCCStateMkdirFailsOnFileParent(t *testing.T) {
	dir := t.TempDir()
	// Create a regular file where SaveTCCState wants to create a parent dir.
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(blocker, "child", "tcc.json")
	err := SaveTCCState(target, &TCCState{})
	if err == nil {
		t.Fatal("SaveTCCState should fail when parent path is a file")
	}
	if !strings.Contains(err.Error(), "create tcc state dir") {
		t.Fatalf("err = %q, want substring 'create tcc state dir'", err.Error())
	}
}

func TestSaveTCCStateFillsZeroVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tcc.json")
	state := &TCCState{Version: 0}
	if err := SaveTCCState(path, state); err != nil {
		t.Fatalf("SaveTCCState: %v", err)
	}
	if state.Version != TCCStateVersion {
		t.Errorf("in-memory Version = %d, want %d", state.Version, TCCStateVersion)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got TCCState
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Version != TCCStateVersion {
		t.Errorf("on-disk Version = %d, want %d", got.Version, TCCStateVersion)
	}
}
