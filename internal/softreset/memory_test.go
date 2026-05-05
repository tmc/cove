package softreset

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestMemoryProbePassesWhenMarkersAreGone(t *testing.T) {
	root := t.TempDir()
	got, err := (MemoryProbe{
		Roots: []string{root},
		Reset: func(context.Context, string) error {
			return os.Remove(filepath.Join(root, "cove-softreset-memory-marker"))
		},
	}).Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Status != StatusPass {
		t.Fatalf("Status = %s, evidence=%v", got.Status, got.Evidence)
	}
	if !hasEvidence(got.Evidence, "markers=absent-after-reset") {
		t.Fatalf("evidence = %v", got.Evidence)
	}
}

func TestMemoryProbeFailsWhenMarkersSurvive(t *testing.T) {
	root := t.TempDir()
	got, err := (MemoryProbe{
		Roots: []string{root},
		Reset: func(context.Context, string) error { return nil },
	}).Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Status != StatusFail {
		t.Fatalf("Status = %s, evidence=%v", got.Status, got.Evidence)
	}
	if !hasEvidence(got.Evidence, "survivors=1") {
		t.Fatalf("evidence = %v", got.Evidence)
	}
}

func TestMemoryProbeLimitsWhenNoMarkersArmed(t *testing.T) {
	got, err := (MemoryProbe{Roots: []string{""}}).Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Status != StatusLimit {
		t.Fatalf("Status = %s, evidence=%v", got.Status, got.Evidence)
	}
	if !hasEvidence(got.Evidence, "markers=not-armed") {
		t.Fatalf("evidence = %v", got.Evidence)
	}
}

func TestMemoryProbeReportsResetError(t *testing.T) {
	want := errors.New("reset failed")
	_, err := (MemoryProbe{
		Roots: []string{t.TempDir()},
		Reset: func(context.Context, string) error { return want },
	}).Run(context.Background())
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}
