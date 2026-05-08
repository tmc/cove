package coved

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tmc/vz-macos/internal/storagecensus"
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
