package vmpolicy

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadMissingReturnsDefault(t *testing.T) {
	p, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !p.Empty() {
		t.Fatalf("Load() = %#v, want empty policy", p)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := Policy{IdleTimeout: 30 * time.Minute, MaxAge: 24 * time.Hour, RunBudget: 100}
	if err := Save(dir, want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, fileName)); err != nil {
		t.Fatalf("Stat(policy.json) error = %v", err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got != want {
		t.Fatalf("Load() = %#v, want %#v", got, want)
	}
}

func TestMergePreservesUnrelatedFields(t *testing.T) {
	base := Policy{IdleTimeout: 30 * time.Minute, MaxAge: 24 * time.Hour, RunBudget: 100}
	got := base.Merge(Policy{RunBudget: 250})
	want := Policy{IdleTimeout: 30 * time.Minute, MaxAge: 24 * time.Hour, RunBudget: 250}
	if got != want {
		t.Fatalf("Merge() = %#v, want %#v", got, want)
	}
}

func TestParseRunBudget(t *testing.T) {
	got, err := ParseRunBudget("12")
	if err != nil {
		t.Fatalf("ParseRunBudget() error = %v", err)
	}
	if got != 12 {
		t.Fatalf("ParseRunBudget() = %d, want 12", got)
	}
	if _, err := ParseRunBudget("0"); err == nil {
		t.Fatal("ParseRunBudget(0) error = nil, want error")
	}
}
