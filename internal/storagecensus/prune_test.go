package storagecensus

import (
	"bytes"
	"context"
	"fmt"
	"strings"
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
		rep, err := CoordinatePrune(context.Background(), nil, 100, 200, false, nil)
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
		rep, err := CoordinatePrune(context.Background(), []Pruner{bs, rn}, 1000, 600, false, nil)
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
		rep, err := CoordinatePrune(context.Background(), []Pruner{bs}, 300, 0, true, nil)
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
		rep, err := CoordinatePrune(context.Background(), []Pruner{bs, errPruner{name: "broken"}}, 100, 50, false, nil)
		if err == nil {
			t.Fatalf("expected error from errPruner")
		}
		if rep.BytesRemoved != 50 || len(rep.Removed) != 1 {
			t.Errorf("expected partial progress, got %+v", rep)
		}
	})
}

type fakePins map[string]bool

func (f fakePins) IsPinned(category, id string) bool { return f[category+":"+id] }

func TestCoordinatePruneSkipsPinnedItems(t *testing.T) {
	t0 := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)

	vms := fakePruner{name: "vms", cs: []Candidate{
		{Path: "/Users/x/.vz/vms/foo", Bytes: 100, LastUsed: t0.Add(-72 * time.Hour), Reason: "old"},
		{Path: "/Users/x/.vz/vms/bar", Bytes: 100, LastUsed: t0.Add(-24 * time.Hour), Reason: "old"},
	}}
	pins := fakePins{"vm:foo": true}
	rep, err := CoordinatePrune(context.Background(), []Pruner{vms}, 300, 100, false, pins)
	if err != nil {
		t.Fatalf("CoordinatePrune: %v", err)
	}
	if len(rep.Removed) != 1 {
		t.Fatalf("Removed len = %d, want 1; got %+v", len(rep.Removed), rep.Removed)
	}
	if rep.Removed[0].Path != "/Users/x/.vz/vms/bar" {
		t.Errorf("Removed[0] = %s, want /Users/x/.vz/vms/bar (foo is pinned)", rep.Removed[0].Path)
	}
	if rep.PinnedSkipped != 1 {
		t.Errorf("PinnedSkipped = %d, want 1", rep.PinnedSkipped)
	}

	// build-scratch has no pin namespace; pins must not affect it even
	// if a "build-scratch:foo" pin is somehow present in the set.
	bs := fakePruner{name: "build-scratch", cs: []Candidate{
		{Path: "/scratch/foo", Bytes: 200, LastUsed: t0.Add(-24 * time.Hour), Reason: "old"},
	}}
	rep, err = CoordinatePrune(context.Background(), []Pruner{bs}, 300, 100, false, fakePins{"build-scratch:foo": true})
	if err != nil {
		t.Fatalf("CoordinatePrune: %v", err)
	}
	if len(rep.Removed) != 1 || rep.PinnedSkipped != 0 {
		t.Errorf("build-scratch should not be filtered: removed=%d pinned-skipped=%d", len(rep.Removed), rep.PinnedSkipped)
	}

	// Nil PinChecker means no filtering even when candidates carry
	// names that would otherwise match a pin.
	rep, err = CoordinatePrune(context.Background(), []Pruner{vms}, 300, 100, false, nil)
	if err != nil {
		t.Fatalf("CoordinatePrune: %v", err)
	}
	if len(rep.Removed) != 2 || rep.PinnedSkipped != 0 {
		t.Errorf("nil PinChecker should pass everything through: removed=%d pinned-skipped=%d", len(rep.Removed), rep.PinnedSkipped)
	}
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
	rep, err := CoordinatePrune(context.Background(), []Pruner{b, a}, 30, 10, false, nil)
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

func TestRenderPruneHuman(t *testing.T) {
	t0 := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		rep  PruneReport
		want []string // substrings that must appear
		miss []string // substrings that must NOT appear
	}{
		{
			name: "empty dry-run",
			rep:  PruneReport{Apply: false, UsedBefore: 100, UsedAfter: 100, Target: 200},
			want: []string{"mode=dry-run", "no candidates selected"},
			miss: []string{"would-remove", "removed:"},
		},
		{
			name: "dry-run with removed and pinned-skipped",
			rep: PruneReport{
				Apply: false, UsedBefore: 1000, UsedAfter: 600, Target: 600,
				Removed: []Candidate{
					{Path: "/a", Bytes: 400, LastUsed: t0, Category: "build-scratch", Reason: "old"},
				},
				BytesRemoved:  400,
				PinnedSkipped: 2,
			},
			want: []string{"mode=dry-run", "would-remove", "build-scratch", "/a", "pinned-skipped: 2", "(re-run with -apply to delete)"},
		},
		{
			name: "apply with skipped",
			rep: PruneReport{
				Apply: true, UsedBefore: 500, UsedAfter: 400, Target: 200,
				Removed: []Candidate{
					{Path: "/ok", Bytes: 100, Category: "runs", Reason: "old"},
				},
				Skipped: []Candidate{
					{Path: "/no-fn", Bytes: 100, Category: "runs", Reason: "no delete fn"},
				},
				BytesRemoved: 100,
			},
			want: []string{"mode=apply", "removed", "skipped", "/ok", "/no-fn"},
			miss: []string{"(re-run with -apply", "no candidates"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := RenderPruneHuman(&buf, tt.rep); err != nil {
				t.Fatalf("RenderPruneHuman: %v", err)
			}
			out := buf.String()
			for _, s := range tt.want {
				if !strings.Contains(out, s) {
					t.Errorf("output missing %q\n--- got ---\n%s", s, out)
				}
			}
			for _, s := range tt.miss {
				if strings.Contains(out, s) {
					t.Errorf("output unexpectedly contains %q\n--- got ---\n%s", s, out)
				}
			}
		})
	}
}

func TestCoordinatePruneCountsCollectErrors(t *testing.T) {
	good := fakePruner{name: "ok", cs: []Candidate{
		{Path: "a", Bytes: 200, LastUsed: time.Unix(1, 0)},
	}}
	bad := errPruner{name: "boom"}
	rep, err := CoordinatePrune(context.Background(), []Pruner{good, bad}, 1000, 0, false, nil)
	if err == nil {
		t.Fatal("CoordinatePrune: want collect error, got nil")
	}
	if rep.CollectErrors != 1 {
		t.Fatalf("CollectErrors = %d, want 1", rep.CollectErrors)
	}
	if len(rep.Removed) != 1 || rep.Removed[0].Path != "a" {
		t.Fatalf("Removed = %#v, want one entry from good pruner", rep.Removed)
	}
}
