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
// tripwire.
//
// It does not mutate state. Phase 3 (prune coordinator) is responsible
// for actual eviction; until that lands the StateHard branch logs a
// "would-prune" hint and emits storage_prune_run with dry_run=true so
// operators can wire alerting on it without waiting for Phase 3.
type StoragePollScheduler struct {
	// Root is the cove root, typically ~/.vz.
	Root string
	// Categories is the descriptor list the census walks. The package
	// main cove binary owns the canonical list; coved accepts it as a
	// dependency to avoid pulling main's filesystem-layout helpers
	// into internal/coved.
	Categories []storagecensus.Descriptor
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
		// TODO(040 Phase 3): once the prune coordinator lands, call it
		// here when budget+pins are configured. Until then we emit a
		// "would-prune" event so alerting hooks can wire up early.
		s.emitPruneWouldRun(ctx, rep)
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

func (s *StoragePollScheduler) emitPruneWouldRun(ctx context.Context, rep storagecensus.Report) {
	if s.Logger != nil {
		s.Logger.Warn("storage budget hard threshold; prune coordinator not yet wired",
			slog.Int64("used_bytes", rep.UsedBytes),
		)
	}
	if s.Bus == nil {
		return
	}
	extra := map[string]any{
		"category":    "all",
		"bytes_freed": int64(0),
		"dry_run":     true,
		"used_bytes":  rep.UsedBytes,
		"reason":      "phase-3-not-shipped",
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
