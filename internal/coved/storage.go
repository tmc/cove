package coved

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	runmetrics "github.com/tmc/vz-macos/internal/metrics"
	"github.com/tmc/vz-macos/internal/storagecensus"
)

// DefaultStoragePollInterval is the cadence the storage poll loop uses
// when StoragePollScheduler.Interval is unset. Design 040 Phase 5.
const DefaultStoragePollInterval = time.Hour

// StoragePollScheduler runs the storage census on a ticker and emits
// metrics events when the report crosses the budget warn or hard
// tripwire. When the hard tripwire fires the scheduler also drives
// the storagecensus prune coordinator across its configured Pruners.
type StoragePollScheduler struct {
	// Root is the cove root, typically ~/.vz.
	Root string
	// Categories is the descriptor list the census walks. The package
	// main cove binary owns the canonical list; coved accepts it as a
	// dependency to avoid pulling main's filesystem-layout helpers
	// into internal/coved.
	Categories []storagecensus.Descriptor
	// Pruners is the list of category-specific pruners the coordinator
	// consults when the hard tripwire fires. When empty the coordinator
	// still runs and emits a no-op storage_prune_run event so alerting
	// hooks see the daemon attempted eviction.
	Pruners []storagecensus.Pruner
	// Apply, when true, lets the coordinator actually delete candidates.
	// Default false keeps the daemon path dry-run only; cmd/coved opts
	// in via the COVE_DAEMON_STORAGE_PRUNE_APPLY env var.
	Apply bool
	// Pins is consulted to filter pinned candidates out before sorting.
	// nil means no pin filter is applied (build-scratch and other
	// non-pinnable categories are unaffected either way).
	Pins storagecensus.PinChecker
	// Interval is the poll cadence. Zero falls back to DefaultStoragePollInterval.
	Interval time.Duration
	// Logger is optional; nil disables logging.
	Logger *slog.Logger
	// Bus is the event bus the daemon publishes through. When nil the
	// scheduler still runs but no events are emitted.
	Bus *EventBus
	// Now is injectable for tests.
	Now func() time.Time
	// Tick is injectable for tests; nil means use time.NewTicker.
	Tick func(time.Duration) <-chan time.Time

	mu        sync.Mutex
	lastUsed  int64
	lastState storagecensus.State
	lastRun   time.Time
	runs      int64
	errors    int64
}

// Errors returns the count of RunOnce calls that failed during the
// census walk. It is informational only and does not include hard
// tripwire prune failures.
func (s *StoragePollScheduler) Errors() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.errors
}

// NewStoragePollScheduler returns a scheduler with the default interval.
func NewStoragePollScheduler(root string, cats []storagecensus.Descriptor, logger *slog.Logger) *StoragePollScheduler {
	return &StoragePollScheduler{
		Root:       root,
		Categories: cats,
		Interval:   DefaultStoragePollInterval,
		Logger:     logger,
		Now:        time.Now,
	}
}

// Run blocks until ctx is canceled, ticking the storage poll at
// Interval. The first tick fires after Interval, mirroring the image
// GC scheduler.
func (s *StoragePollScheduler) Run(ctx context.Context) {
	interval := s.Interval
	if interval <= 0 {
		interval = DefaultStoragePollInterval
	}
	var tickC <-chan time.Time
	var stop func()
	if s.Tick != nil {
		tickC = s.Tick(interval)
	} else {
		t := time.NewTicker(interval)
		tickC = t.C
		stop = t.Stop
	}
	if stop != nil {
		defer stop()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-tickC:
			if _, err := s.RunOnce(ctx); err != nil && s.Logger != nil {
				s.Logger.Debug("scheduled storage poll", slog.Any("err", err))
			}
		}
	}
}

// RunOnce executes one census walk and emits any tripwire events.
// It is safe to call concurrently with Run; mu serializes state
// updates and event emission.
func (s *StoragePollScheduler) RunOnce(ctx context.Context) (storagecensus.Report, error) {
	if err := ctx.Err(); err != nil {
		return storagecensus.Report{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Root == "" {
		return storagecensus.Report{}, fmt.Errorf("storage poll: root required")
	}
	rep, err := storagecensus.Walk(s.Root, s.Categories, storagecensus.Options{TopN: 0})
	if err != nil {
		s.errors++
		return storagecensus.Report{}, fmt.Errorf("storage poll: %w", err)
	}
	if b, berr := storagecensus.LoadBudget(s.Root); berr == nil && b.IsSet() {
		bb := b
		rep.Budget = &bb
	}
	state := rep.State()
	s.lastUsed = rep.UsedBytes
	s.lastState = state
	s.lastRun = s.now()
	s.runs++
	switch state {
	case storagecensus.StateWarn:
		s.emit(ctx, "storage_budget_warn", rep)
	case storagecensus.StateHard:
		s.emit(ctx, "storage_budget_hard", rep)
		s.runPrune(ctx, rep)
	}
	return rep, nil
}

// Stats returns a snapshot of the most recent poll.
func (s *StoragePollScheduler) Stats() (used int64, state storagecensus.State, lastRun time.Time, runs int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastUsed, s.lastState, s.lastRun, s.runs
}

func (s *StoragePollScheduler) emit(ctx context.Context, eventType string, rep storagecensus.Report) {
	if s.Logger != nil {
		s.Logger.Info("storage budget tripwire",
			slog.String("event_type", eventType),
			slog.Int64("used_bytes", rep.UsedBytes),
			slog.String("state", rep.State().String()),
		)
	}
	if s.Bus == nil {
		return
	}
	extra := map[string]any{
		"used_bytes": rep.UsedBytes,
		"state":      rep.State().String(),
	}
	if rep.Budget != nil {
		extra["target_bytes"] = rep.Budget.TargetBytes
		extra["warn_pct"] = rep.Budget.WarnPct
		extra["hard_pct"] = rep.Budget.HardPct
	}
	s.Bus.Publish(ctx, runmetrics.Event{
		Timestamp: s.now().UTC().Format(time.RFC3339Nano),
		EventType: eventType,
		Status:    "ok",
		Extra:     extra,
	})
}

// runPrune drives the storagecensus prune coordinator. It is called
// only when the hard tripwire fires and rep.Budget is set. The
// coordinator handles empty Pruners gracefully (no-op plan); a partial
// per-pruner failure still yields a usable report and an emitted
// event.
func (s *StoragePollScheduler) runPrune(ctx context.Context, rep storagecensus.Report) {
	if rep.Budget == nil {
		return
	}
	pruneRep, err := storagecensus.CoordinatePrune(ctx, s.Pruners, rep.UsedBytes, rep.Budget.TargetBytes, s.Apply, s.Pins)
	if err != nil && s.Logger != nil {
		s.Logger.Warn("storage prune coordinator partial failure", slog.Any("err", err))
	}
	if s.Logger != nil {
		s.Logger.Info("storage prune coordinator",
			slog.Bool("apply", s.Apply),
			slog.Int64("used_bytes_before", pruneRep.UsedBefore),
			slog.Int64("used_bytes_after", pruneRep.UsedAfter),
			slog.Int64("bytes_freed", pruneRep.BytesRemoved),
			slog.Int("removed_count", len(pruneRep.Removed)),
			slog.Int("skipped_count", len(pruneRep.Skipped)),
		)
	}
	if s.Bus == nil {
		return
	}
	extra := map[string]any{
		"category":          "all",
		"bytes_freed":       pruneRep.BytesRemoved,
		"dry_run":           !s.Apply,
		"used_bytes_before": pruneRep.UsedBefore,
		"used_bytes_after":  pruneRep.UsedAfter,
		"removed_count":     int64(len(pruneRep.Removed)),
		"skipped_count":     int64(len(pruneRep.Skipped)),
	}
	if len(s.Pruners) == 0 {
		extra["reason"] = "no-pruners-configured"
	}
	s.Bus.Publish(ctx, runmetrics.Event{
		Timestamp: s.now().UTC().Format(time.RFC3339Nano),
		EventType: "storage_prune_run",
		Status:    "ok",
		Extra:     extra,
	})
}

func (s *StoragePollScheduler) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
