package coved

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/vz-macos/internal/storagecensus"
	"github.com/tmc/vz-macos/internal/storagepins"
)

func TestStoragePollSchedulerEmitsTripwires(t *testing.T) {
	root := t.TempDir()

	// Synthetic categories: "vms" with a 200-byte file. The budget has
	// target=400 / warn=25% (=100B) / hard=75% (=300B), so a 200-byte
	// payload is in StateWarn. Bumping to a 400-byte payload crosses
	// hard.
	vmsDir := filepath.Join(root, "vms")
	if err := os.MkdirAll(vmsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writePayload := func(size int) {
		if err := os.WriteFile(filepath.Join(vmsDir, "disk.img"), make([]byte, size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := storagecensus.SaveBudget(root, storagecensus.Budget{
		TargetBytes: 400,
		WarnPct:     25,
		HardPct:     75,
	}); err != nil {
		t.Fatal(err)
	}

	bus := NewEventBus(16)
	sub, cancel := bus.Subscribe(8)
	defer cancel()

	tickCh := make(chan time.Time, 4)
	sched := &StoragePollScheduler{
		Root:       root,
		Categories: []storagecensus.Descriptor{{Name: "vms", Path: vmsDir}},
		Interval:   time.Millisecond,
		Bus:        bus,
		Now:        func() time.Time { return time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC) },
		Tick:       func(time.Duration) <-chan time.Time { return tickCh },
	}

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	done := make(chan struct{})
	go func() {
		sched.Run(ctx)
		close(done)
	}()

	collect := func(want int, timeout time.Duration) []Event {
		t.Helper()
		var got []Event
		deadline := time.After(timeout)
		for len(got) < want {
			select {
			case ev := <-sub:
				got = append(got, ev)
			case <-deadline:
				t.Fatalf("collected %d events, want %d", len(got), want)
			}
		}
		return got
	}

	// Tick 1: 200 bytes → StateWarn. One event.
	writePayload(200)
	tickCh <- time.Now()
	warnEvents := collect(1, 2*time.Second)
	if got := warnEvents[0].EventType; got != "storage_budget_warn" {
		t.Errorf("tick 1 event_type = %q, want storage_budget_warn", got)
	}
	if got := warnEvents[0].Extra["state"]; got != "warn" {
		t.Errorf("tick 1 state = %v, want warn", got)
	}

	// Tick 2: 400 bytes → StateHard. Two events: hard tripwire +
	// would-prune dry-run.
	writePayload(400)
	tickCh <- time.Now()
	hardEvents := collect(2, 2*time.Second)
	if got := hardEvents[0].EventType; got != "storage_budget_hard" {
		t.Errorf("tick 2 event_type = %q, want storage_budget_hard", got)
	}
	if got := hardEvents[0].Extra["state"]; got != "hard" {
		t.Errorf("tick 2 state = %v, want hard", got)
	}
	if got := hardEvents[1].EventType; got != "storage_prune_run" {
		t.Errorf("tick 2 event[1].event_type = %q, want storage_prune_run", got)
	}
	if got := hardEvents[1].Extra["dry_run"]; got != true {
		t.Errorf("tick 2 dry_run = %v, want true", got)
	}
	if got := hardEvents[1].Extra["category"]; got != "all" {
		t.Errorf("tick 2 category = %v, want all", got)
	}

	stop()
	<-done

	// Final state matches the last tick.
	used, state, _, runs := sched.Stats()
	if used != 400 {
		t.Errorf("Stats() used = %d, want 400", used)
	}
	if state != storagecensus.StateHard {
		t.Errorf("Stats() state = %s, want hard", state)
	}
	if runs != 2 {
		t.Errorf("Stats() runs = %d, want 2", runs)
	}
}

// TestStoragePollSchedulerNoBudgetIsQuiet confirms that without a
// configured budget the scheduler emits zero tripwire events even when
// the tree has data.
func TestStoragePollSchedulerNoBudgetIsQuiet(t *testing.T) {
	root := t.TempDir()
	vmsDir := filepath.Join(root, "vms")
	if err := os.MkdirAll(vmsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vmsDir, "disk.img"), make([]byte, 1024), 0o644); err != nil {
		t.Fatal(err)
	}

	bus := NewEventBus(8)
	sub, cancel := bus.Subscribe(4)
	defer cancel()

	tickCh := make(chan time.Time, 1)
	sched := &StoragePollScheduler{
		Root:       root,
		Categories: []storagecensus.Descriptor{{Name: "vms", Path: vmsDir}},
		Interval:   time.Millisecond,
		Bus:        bus,
		Now:        func() time.Time { return time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC) },
		Tick:       func(time.Duration) <-chan time.Time { return tickCh },
	}

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	done := make(chan struct{})
	go func() {
		sched.Run(ctx)
		close(done)
	}()

	tickCh <- time.Now()

	select {
	case ev := <-sub:
		t.Errorf("unexpected event without budget: %+v", ev)
	case <-time.After(200 * time.Millisecond):
		// good
	}

	stop()
	<-done

	_, state, _, runs := sched.Stats()
	if state != storagecensus.StateUnset {
		t.Errorf("Stats() state = %s, want unset", state)
	}
	if runs != 1 {
		t.Errorf("Stats() runs = %d, want 1", runs)
	}
}

// fakePruner is a test double that returns its configured candidates
// and never deletes anything. The coordinator will fall back to the
// "candidate has no Delete fn" path on apply, but the dry-run case
// (which is what this test exercises) reports them as Removed.
type fakePruner struct {
	name string
	cs   []storagecensus.Candidate
}

func (f fakePruner) Name() string { return f.name }
func (f fakePruner) Candidates(_ context.Context) ([]storagecensus.Candidate, error) {
	return f.cs, nil
}

// TestStoragePollSchedulerHardInvokesPrunerDryRun confirms that when
// the hard tripwire fires the scheduler runs the storagecensus prune
// coordinator across its configured Pruners and emits a
// storage_prune_run event whose extras reflect the coordinator report
// (bytes_freed, dry_run=true by default, used_bytes_before/after).
func TestStoragePollSchedulerHardInvokesPrunerDryRun(t *testing.T) {
	root := t.TempDir()
	vmsDir := filepath.Join(root, "vms")
	if err := os.MkdirAll(vmsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vmsDir, "disk.img"), make([]byte, 400), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := storagecensus.SaveBudget(root, storagecensus.Budget{
		TargetBytes: 200,
		WarnPct:     50, // 100B
		HardPct:     90, // 180B; 400B payload → hard
	}); err != nil {
		t.Fatal(err)
	}

	t0 := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	pruner := fakePruner{
		name: "build-scratch",
		cs: []storagecensus.Candidate{
			{Path: "/fake/old", Bytes: 150, LastUsed: t0.Add(-72 * time.Hour), Reason: "old"},
			{Path: "/fake/older", Bytes: 100, LastUsed: t0.Add(-96 * time.Hour), Reason: "older"},
		},
	}

	bus := NewEventBus(16)
	sub, cancel := bus.Subscribe(8)
	defer cancel()

	tickCh := make(chan time.Time, 1)
	sched := &StoragePollScheduler{
		Root:       root,
		Categories: []storagecensus.Descriptor{{Name: "vms", Path: vmsDir}},
		Pruners:    []storagecensus.Pruner{pruner},
		Apply:      false,
		Interval:   time.Millisecond,
		Bus:        bus,
		Now:        func() time.Time { return t0 },
		Tick:       func(time.Duration) <-chan time.Time { return tickCh },
	}

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	done := make(chan struct{})
	go func() {
		sched.Run(ctx)
		close(done)
	}()

	tickCh <- time.Now()

	// Two events: hard tripwire + storage_prune_run.
	var events []Event
	deadline := time.After(2 * time.Second)
	for len(events) < 2 {
		select {
		case ev := <-sub:
			events = append(events, ev)
		case <-deadline:
			t.Fatalf("collected %d events, want 2", len(events))
		}
	}
	if events[0].EventType != "storage_budget_hard" {
		t.Errorf("event[0].EventType = %q, want storage_budget_hard", events[0].EventType)
	}
	pe := events[1]
	if pe.EventType != "storage_prune_run" {
		t.Fatalf("event[1].EventType = %q, want storage_prune_run", pe.EventType)
	}
	// Coordinator picks oldest first: /fake/older (100B) then /fake/old
	// (150B). Used 400 → target 200 means 200B to reclaim; 100+150=250
	// covers it.
	if got := pe.Extra["dry_run"]; got != true {
		t.Errorf("dry_run = %v, want true", got)
	}
	if got := pe.Extra["bytes_freed"]; got != int64(250) {
		t.Errorf("bytes_freed = %v, want 250", got)
	}
	if got := pe.Extra["used_bytes_before"]; got != int64(400) {
		t.Errorf("used_bytes_before = %v, want 400", got)
	}
	if got := pe.Extra["used_bytes_after"]; got != int64(150) {
		t.Errorf("used_bytes_after = %v, want 150", got)
	}
	if got := pe.Extra["removed_count"]; got != int64(2) {
		t.Errorf("removed_count = %v, want 2", got)
	}
	if got := pe.Extra["category"]; got != "all" {
		t.Errorf("category = %v, want all", got)
	}
	if got, ok := pe.Extra["pinned_skipped"]; !ok || got != int64(0) {
		t.Errorf("pinned_skipped = %v (ok=%v), want 0", got, ok)
	}
	if got, ok := pe.Extra["collect_errors"]; !ok || got != int64(0) {
		t.Errorf("collect_errors = %v (ok=%v), want 0", got, ok)
	}

	stop()
	<-done
}

// fakePins is a test PinChecker that pins a fixed canonical "ns:id" set.
type fakePins map[string]bool

func (f fakePins) IsPinned(category, id string) bool { return f[category+":"+id] }

type tempDirPruner struct {
	name string
	root string
}

func (p tempDirPruner) Name() string { return p.name }

func (p tempDirPruner) Candidates(context.Context) ([]storagecensus.Candidate, error) {
	entries, err := os.ReadDir(p.root)
	if err != nil {
		return nil, err
	}
	var out []storagecensus.Candidate
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(p.root, e.Name())
		pth := path
		out = append(out, storagecensus.Candidate{
			Path:     path,
			Bytes:    dirSize(path),
			LastUsed: time.Unix(0, 0),
			Reason:   "test",
			Delete:   func() error { return os.RemoveAll(pth) },
		})
	}
	return out, nil
}

func TestStoragePollSchedulerPinnedPruneUsesPinFile(t *testing.T) {
	tests := []struct {
		name          string
		pinKeep       bool
		wantKeep      bool
		wantPinnedNum int64
	}{
		{"pinned keep is skipped", true, true, 1},
		{"unpinned keep can be pruned", false, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			vmsDir := filepath.Join(root, "vms")
			for _, name := range []string{"keep", "drop"} {
				dir := filepath.Join(vmsDir, name)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "disk.img"), make([]byte, 200), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := storagecensus.SaveBudget(root, storagecensus.Budget{TargetBytes: 100, WarnPct: 10, HardPct: 50}); err != nil {
				t.Fatal(err)
			}
			pins := storagepins.New()
			if tt.pinKeep {
				if err := pins.Add("vm", "keep", time.Unix(0, 0)); err != nil {
					t.Fatal(err)
				}
			}
			if err := storagepins.Save(root, pins); err != nil {
				t.Fatal(err)
			}
			loaded, err := storagepins.Load(root)
			if err != nil {
				t.Fatal(err)
			}

			bus := NewEventBus(8)
			s := &StoragePollScheduler{
				Root:       root,
				Categories: []storagecensus.Descriptor{{Name: "vms", Path: vmsDir}},
				Pruners:    []storagecensus.Pruner{tempDirPruner{name: "vms", root: vmsDir}},
				Pins:       loaded,
				Apply:      true,
				Bus:        bus,
				Now:        func() time.Time { return time.Unix(1, 0) },
			}
			if _, err := s.RunOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			tail := bus.Tail()
			ev := tail[len(tail)-1]
			if ev.EventType != "storage_prune_run" || ev.Extra["pinned_skipped"] != tt.wantPinnedNum {
				t.Fatalf("event = %+v, want pinned_skipped %d", ev, tt.wantPinnedNum)
			}
			if _, err := os.Stat(filepath.Join(vmsDir, "keep")); os.IsNotExist(err) == tt.wantKeep {
				t.Fatalf("keep exists = %v, want %v", !os.IsNotExist(err), tt.wantKeep)
			}
			if _, err := os.Stat(filepath.Join(vmsDir, "drop")); !os.IsNotExist(err) {
				t.Fatalf("drop still exists: %v", err)
			}
		})
	}
}

// TestStoragePollSchedulerHonorsPins confirms that when the hard
// tripwire fires with a non-nil Pins, the prune coordinator excludes
// pinned candidates from the eviction plan and surfaces the count via
// the storage_prune_run event extras.
func TestStoragePollSchedulerHonorsPins(t *testing.T) {
	root := t.TempDir()
	vmsDir := filepath.Join(root, "vms")
	if err := os.MkdirAll(vmsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vmsDir, "disk.img"), make([]byte, 400), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := storagecensus.SaveBudget(root, storagecensus.Budget{
		TargetBytes: 200,
		WarnPct:     50,
		HardPct:     90,
	}); err != nil {
		t.Fatal(err)
	}

	t0 := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	pruner := fakePruner{
		name: "vms",
		cs: []storagecensus.Candidate{
			// pinned: must be excluded from Removed and counted as PinnedSkipped.
			{Path: "/Users/x/.vz/vms/keep", Bytes: 150, LastUsed: t0.Add(-96 * time.Hour), Reason: "older"},
			// not pinned: should be selected.
			{Path: "/Users/x/.vz/vms/drop", Bytes: 250, LastUsed: t0.Add(-72 * time.Hour), Reason: "old"},
		},
	}

	bus := NewEventBus(16)
	sub, cancel := bus.Subscribe(8)
	defer cancel()

	tickCh := make(chan time.Time, 1)
	sched := &StoragePollScheduler{
		Root:       root,
		Categories: []storagecensus.Descriptor{{Name: "vms", Path: vmsDir}},
		Pruners:    []storagecensus.Pruner{pruner},
		Pins:       fakePins{"vm:keep": true},
		Apply:      false,
		Interval:   time.Millisecond,
		Bus:        bus,
		Now:        func() time.Time { return t0 },
		Tick:       func(time.Duration) <-chan time.Time { return tickCh },
	}

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	done := make(chan struct{})
	go func() {
		sched.Run(ctx)
		close(done)
	}()

	tickCh <- time.Now()

	var events []Event
	deadline := time.After(2 * time.Second)
	for len(events) < 2 {
		select {
		case ev := <-sub:
			events = append(events, ev)
		case <-deadline:
			t.Fatalf("collected %d events, want 2", len(events))
		}
	}
	pe := events[1]
	if pe.EventType != "storage_prune_run" {
		t.Fatalf("event[1].EventType = %q, want storage_prune_run", pe.EventType)
	}
	// Pinned /keep (150B) is excluded; only /drop (250B) is selected.
	if got := pe.Extra["bytes_freed"]; got != int64(250) {
		t.Errorf("bytes_freed = %v, want 250", got)
	}
	if got := pe.Extra["removed_count"]; got != int64(1) {
		t.Errorf("removed_count = %v, want 1 (pinned /keep excluded)", got)
	}

	stop()
	<-done
}

// TestStoragePollSchedulerRunOnceErrors covers the two RunOnce error
// paths: a canceled context returns ctx.Err() before any work, and an
// empty Root surfaces a "root required" failure.
func TestStoragePollSchedulerRunOnceErrors(t *testing.T) {
	tests := []struct {
		name    string
		setup   func() (*StoragePollScheduler, context.Context)
		wantErr string
	}{
		{
			name: "ctx canceled",
			setup: func() (*StoragePollScheduler, context.Context) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return &StoragePollScheduler{Root: t.TempDir()}, ctx
			},
			wantErr: "context canceled",
		},
		{
			name: "empty root",
			setup: func() (*StoragePollScheduler, context.Context) {
				return &StoragePollScheduler{}, context.Background()
			},
			wantErr: "root required",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, ctx := tc.setup()
			_, err := s.RunOnce(ctx)
			if err == nil {
				t.Fatalf("RunOnce err = nil, want %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("RunOnce err = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestStoragePollSchedulerHardWithoutPrunersAnnotates confirms the
// scheduler still drives the prune coordinator when Pruners is empty
// and tags the emitted storage_prune_run event with
// reason=no-pruners-configured so alerting hooks can distinguish a
// dry-run-by-design from a misconfiguration.
func TestStoragePollSchedulerHardWithoutPrunersAnnotates(t *testing.T) {
	root := t.TempDir()
	vmsDir := filepath.Join(root, "vms")
	if err := os.MkdirAll(vmsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(vmsDir, "disk.img"), make([]byte, 400), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := storagecensus.SaveBudget(root, storagecensus.Budget{
		TargetBytes: 200, WarnPct: 50, HardPct: 90,
	}); err != nil {
		t.Fatal(err)
	}

	bus := NewEventBus(8)
	sub, cancel := bus.Subscribe(4)
	defer cancel()

	sched := &StoragePollScheduler{
		Root:       root,
		Categories: []storagecensus.Descriptor{{Name: "vms", Path: vmsDir}},
		Bus:        bus,
		Now:        func() time.Time { return time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC) },
	}
	if _, err := sched.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	var events []Event
	deadline := time.After(2 * time.Second)
	for len(events) < 2 {
		select {
		case ev := <-sub:
			events = append(events, ev)
		case <-deadline:
			t.Fatalf("collected %d events, want 2", len(events))
		}
	}
	pe := events[1]
	if pe.EventType != "storage_prune_run" {
		t.Fatalf("event[1].EventType = %q, want storage_prune_run", pe.EventType)
	}
	if got := pe.Extra["reason"]; got != "no-pruners-configured" {
		t.Errorf("reason = %v, want no-pruners-configured", got)
	}
	if got := pe.Extra["bytes_freed"]; got != int64(0) {
		t.Errorf("bytes_freed = %v, want 0", got)
	}
}

func TestStoragePollSchedulerCountsErrors(t *testing.T) {
	root := t.TempDir()
	// Force storagecensus.Walk to fail by pointing a descriptor at a
	// non-directory file.
	bad := filepath.Join(root, "bad-cat")
	if err := os.Mkdir(bad, 0); err != nil {
		t.Fatalf("mkdir mode 0: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(bad, 0700) })

	s := &StoragePollScheduler{
		Root:       root,
		Categories: []storagecensus.Descriptor{{Name: "x", Path: bad}},
		Now:        func() time.Time { return time.Unix(0, 0) },
	}
	if got := s.Errors(); got != 0 {
		t.Fatalf("initial Errors() = %d, want 0", got)
	}
	if _, err := s.RunOnce(context.Background()); err == nil {
		t.Fatal("RunOnce: want error from unreadable category")
	}
	if got := s.Errors(); got != 1 {
		t.Fatalf("Errors() = %d, want 1", got)
	}
}
