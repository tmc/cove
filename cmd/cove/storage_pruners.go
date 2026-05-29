package main

import (
	"context"
	"os"
	"time"

	"github.com/tmc/cove/internal/storagecensus"
)

// buildScratchPruner adapts pruneBuildScratch to the storagecensus.Pruner
// shape. It reuses the existing safety predicates (sanity floor, live-pid)
// by running pruneBuildScratch in dry-run mode; the coordinator drives
// the actual deletion through Candidate.Delete.
type buildScratchPruner struct {
	Root      string
	OlderThan time.Duration
	IsLive    func(int) bool
	Now       func() time.Time
}

func (p buildScratchPruner) Name() string { return "build-scratch" }

func (p buildScratchPruner) Candidates(_ context.Context) ([]storagecensus.Candidate, error) {
	rep, err := pruneBuildScratch(p.Root, p.OlderThan, false, p.IsLive, p.Now)
	if err != nil {
		return nil, err
	}
	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	cs := make([]storagecensus.Candidate, 0, len(rep.Entries))
	for _, e := range rep.Entries {
		if e.Reason != "candidate" {
			continue
		}
		dir := e.Dir
		cs = append(cs, storagecensus.Candidate{
			Path:     dir,
			Bytes:    e.Bytes,
			LastUsed: now().Add(-e.Age),
			Reason:   "build-scratch older-than+pid-dead",
			Delete:   func() error { return os.RemoveAll(dir) },
		})
	}
	return cs, nil
}
