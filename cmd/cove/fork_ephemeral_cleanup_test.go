package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCleanupEphemeralForkEmptyPath(t *testing.T) {
	err := CleanupEphemeralFork("")
	if err == nil || !strings.Contains(err.Error(), "cleanup path required") {
		t.Fatalf("err = %v, want cleanup path required", err)
	}
}

func TestCleanupEphemeralForkRefusesRoot(t *testing.T) {
	err := CleanupEphemeralFork("/")
	if err == nil || !strings.Contains(err.Error(), "refusing to remove") {
		t.Fatalf("err = %v, want refusing to remove", err)
	}
}

func TestCleanupEphemeralForkRemovesWithSentinel(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "child")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ephemeral"), nil, 0644); err != nil {
		t.Fatalf("WriteFile sentinel: %v", err)
	}
	if err := CleanupEphemeralFork(dir); err != nil {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("dir still exists: err = %v", err)
	}
}
