package storagecensus

import (
	"context"
	"fmt"
	"testing"
	"time"
)

type fakePruner struct {
	name string
	cs   []Candidate
}

func (f fakePruner) Name() string                                         { return f.name }
func (f fakePruner) Candidates(_ context.Context) ([]Candidate, error)    { return f.cs, nil }

type errPruner struct {
	name string
}

func (e errPruner) Name() string                                       { return e.name }
func (e errPruner) Candidates(_ context.Context) ([]Candidate, error)  { return nil, fmt.Errorf("boom") }

func TestCoordinatePruneSelectsOldestUntilTarget(t *testing.T) {
	t0 := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)

	t.Run("under target is no-op", func(t *testing.T) {
		rep, err := CoordinatePrune(context.Background(), nil, 100, 200, false)
		if err != nil {
			t.Fatalf("CoordinatePrune: %v", err)
		}
		if rep.UsedAfter != 100 || rep.BytesRemoved != 0 || len(rep.Removed) != 0 {
			t.Errorf("expected no-op, got %+v", rep)
		}
	})

	t.Run("dry-run picks oldest across categories", func(t *testing.T) {
		// Used 1000, target 600, want 400 freed.
		// Candidates spread across two pruners.
		bs := fakePruner{name: "build-scratch", cs: []Candidate{
			{Path: "/a", Bytes: 200, LastUsed: t0.Add(-72 * time.Hour), Reason: "old"},
			{Path: "/b", Bytes: 200, LastUsed: t0.Add(-1 * time.Hour), Reason: "fresh"},
		}}
		rn := fakePruner{name: "runs", cs: []Candidate{
			{Path: "/r1", Bytes: 200, LastUsed: t0.Add(-48 * time.Hour), Reason: "stale"},
			{Path: "/r2", Bytes: 200, LastUsed: t0.Add(-2 * time.Hour), Reason: "fresh"},
		}}
		rep, err := CoordinatePrune(context.Background(), []Pruner{bs, rn}, 1000, 600, false)
		if err != nil {
			t.Fatalf("CoordinatePrune: %v", err)
		}
		if rep.BytesRemoved != 400 {
			t.Errorf("BytesRemoved = %d, want 400", rep.BytesRemoved)
		}
		if rep.UsedAfter != 600 {
			t.Errorf("UsedAfter = %d, want 600", rep.UsedAfter)
		}
		if len(rep.Removed) != 2 {
			t.Fatalf("Removed len = %d, want 2", len(rep.Removed))
		}
		if rep.Removed[0].Path != "/a" {
			t.Errorf("first removed = %s, want /a (oldest)", rep.Removed[0].Path)
		}
		if rep.Removed[1].Path != "/r1" {
			t.Errorf("second removed = %s, want /r1 (next-oldest)", rep.Removed[1].Path)
		}
		if rep.Removed[0].Category != "build-scratch" || rep.Removed[1].Category != "runs" {
			t.Errorf("category labels not set: %+v", rep.Removed)
		}
	})

	t.Run("apply mode runs Delete and skips on error", func(t *testing.T) {
		var deleted []string
		bs := fakePruner{name: "build-scratch", cs: []Candidate{
			{Path: "/ok", Bytes: 100, LastUsed: t0.Add(-48 * time.Hour), Reason: "old",
				Delete: func() error { deleted = append(deleted, "/ok"); return nil }},
			{Path: "/fail", Bytes: 100, LastUsed: t0.Add(-24 * time.Hour), Reason: "old",
				Delete: func() error { return fmt.Errorf("permission denied") }},
			{Path: "/no-delete-fn", Bytes: 100, LastUsed: t0.Add(-12 * time.Hour), Reason: "old",
				Delete: nil},
		}}
		rep, err := CoordinatePrune(context.Background(), []Pruner{bs}, 300, 0, true)
		if err != nil {
			t.Fatalf("CoordinatePrune: %v", err)
		}
		if len(deleted) != 1 || deleted[0] != "/ok" {
			t.Errorf("Delete() called for %v, want only /ok", deleted)
		}
		if len(rep.Removed) != 1 || rep.Removed[0].Path != "/ok" {
			t.Errorf("Removed = %+v, want one entry /ok", rep.Removed)
		}
		if len(rep.Skipped) != 2 {
			t.Errorf("Skipped len = %d, want 2", len(rep.Skipped))
		}
		if rep.BytesRemoved != 100 {
			t.Errorf("BytesRemoved = %d, want 100", rep.BytesRemoved)
		}
	})

	t.Run("collects pruner errors but proceeds", func(t *testing.T) {
		bs := fakePruner{name: "build-scratch", cs: []Candidate{
			{Path: "/a", Bytes: 50, LastUsed: t0.Add(-1 * time.Hour), Reason: "old"},
		}}
		rep, err := CoordinatePrune(context.Background(), []Pruner{bs, errPruner{name: "broken"}}, 100, 50, false)
		if err == nil {
			t.Fatalf("expected error from errPruner")
		}
		if rep.BytesRemoved != 50 || len(rep.Removed) != 1 {
			t.Errorf("expected partial progress, got %+v", rep)
		}
	})
}

func TestCoordinatePruneStableOrdering(t *testing.T) {
	// Two candidates with identical timestamps: order should be path-asc
	// then category-asc.
	t0 := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	a := fakePruner{name: "alpha", cs: []Candidate{
		{Path: "/same", Bytes: 10, LastUsed: t0, Reason: "x"},
	}}
	b := fakePruner{name: "beta", cs: []Candidate{
		{Path: "/same", Bytes: 10, LastUsed: t0, Reason: "x"},
	}}
	rep, err := CoordinatePrune(context.Background(), []Pruner{b, a}, 30, 10, false)
	if err != nil {
		t.Fatalf("CoordinatePrune: %v", err)
	}
	if len(rep.Removed) != 2 {
		t.Fatalf("Removed len = %d, want 2", len(rep.Removed))
	}
	if rep.Removed[0].Category != "alpha" || rep.Removed[1].Category != "beta" {
		t.Errorf("tie-break order = %s,%s; want alpha,beta",
			rep.Removed[0].Category, rep.Removed[1].Category)
	}
}
