package storagecensus

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadBudgetMissingReturnsZero(t *testing.T) {
	root := t.TempDir()
	b, err := LoadBudget(root)
	if err != nil {
		t.Fatalf("LoadBudget on empty dir: %v", err)
	}
	if b.IsSet() {
		t.Errorf("zero-value budget reports IsSet")
	}
	if got := b.TargetBytes; got != 0 {
		t.Errorf("TargetBytes = %d, want 0", got)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	root := t.TempDir()
	want := Budget{TargetBytes: 500 * 1024 * 1024 * 1024, WarnPct: 80, HardPct: 95}
	if err := SaveBudget(root, want); err != nil {
		t.Fatalf("SaveBudget: %v", err)
	}
	got, err := LoadBudget(root)
	if err != nil {
		t.Fatalf("LoadBudget: %v", err)
	}
	if got != want {
		t.Errorf("round-trip = %+v, want %+v", got, want)
	}
	// File should exist at the expected path.
	if _, err := os.Stat(filepath.Join(root, BudgetFilename)); err != nil {
		t.Errorf("storage-budget.json missing: %v", err)
	}
}

func TestClearBudgetRemovesFile(t *testing.T) {
	root := t.TempDir()
	if err := SaveBudget(root, Budget{TargetBytes: 1024}); err != nil {
		t.Fatalf("SaveBudget: %v", err)
	}
	if err := ClearBudget(root); err != nil {
		t.Fatalf("ClearBudget: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, BudgetFilename)); !os.IsNotExist(err) {
		t.Errorf("file still present after Clear: %v", err)
	}
	// Clear is idempotent.
	if err := ClearBudget(root); err != nil {
		t.Errorf("Clear on missing file: %v", err)
	}
}

func TestSaveRejectsInvalid(t *testing.T) {
	root := t.TempDir()
	cases := []Budget{
		{TargetBytes: -1},
		{TargetBytes: 1024, WarnPct: 150},
		{TargetBytes: 1024, HardPct: -5},
		{TargetBytes: 1024, WarnPct: 90, HardPct: 80}, // warn > hard
	}
	for _, b := range cases {
		if err := SaveBudget(root, b); err == nil {
			t.Errorf("SaveBudget(%+v) succeeded; want error", b)
		}
	}
	// File must not have been written.
	if _, err := os.Stat(filepath.Join(root, BudgetFilename)); !os.IsNotExist(err) {
		t.Errorf("invalid input wrote file: %v", err)
	}
}

func TestThresholdHelpers(t *testing.T) {
	b := Budget{TargetBytes: 1000, WarnPct: 80, HardPct: 95}
	if got := b.WarnBytes(); got != 800 {
		t.Errorf("WarnBytes = %d, want 800", got)
	}
	if got := b.HardBytes(); got != 950 {
		t.Errorf("HardBytes = %d, want 950", got)
	}
	zero := Budget{}
	if got := zero.WarnBytes(); got != 0 {
		t.Errorf("zero WarnBytes = %d, want 0", got)
	}
	if got := zero.HardBytes(); got != 0 {
		t.Errorf("zero HardBytes = %d, want 0", got)
	}
}

func TestLoadBudgetCorruptFileReturnsError(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, BudgetFilename), []byte("not json"), 0o644); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}
	if _, err := LoadBudget(root); err == nil {
		t.Errorf("LoadBudget on corrupt file succeeded; want error")
	}
}
