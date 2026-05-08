package storagecensus

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"
	"time"
)

// Candidate names one item a Pruner is willing to delete.
type Candidate struct {
	Path     string
	Bytes    int64
	LastUsed time.Time
	Category string // filled by the coordinator from Pruner.Name()
	Reason   string
	// Delete removes the on-disk item. It must be safe to call once.
	// Pruners that report a Candidate without a Delete fn (e.g. for
	// dry-run readability) are silently skipped on apply.
	Delete func() error
}

// Pruner enumerates removable items from one storage category. Coordinator
// only ever reads Candidates; deletion goes through Candidate.Delete so
// each Pruner controls its own safety predicates.
type Pruner interface {
	// Name is the category label, e.g. "build-scratch".
	Name() string
	// Candidates returns items the Pruner is currently willing to remove.
	// The returned slice is allowed to be empty; ctx is honored on
	// best-effort.
	Candidates(ctx context.Context) ([]Candidate, error)
}

// PruneReport is the result of one CoordinatePrune call.
type PruneReport struct {
	Apply        bool
	UsedBefore   int64
	UsedAfter    int64
	Target       int64
	Removed      []Candidate
	Skipped      []Candidate // selected but unable to delete (apply mode only)
	BytesRemoved int64
}

// CoordinatePrune walks pruners and selects the oldest candidates across
// all categories until enough bytes are reclaimed to drop usedBytes below
// target. With apply=false the report describes the plan and nothing is
// removed. The selection is stable: ties on LastUsed break by Path then
// Category so two runs against the same on-disk state pick the same set.
func CoordinatePrune(ctx context.Context, pruners []Pruner, usedBytes, target int64, apply bool) (PruneReport, error) {
	rep := PruneReport{
		Apply:      apply,
		UsedBefore: usedBytes,
		UsedAfter:  usedBytes,
		Target:     target,
	}
	if usedBytes <= target {
		return rep, nil
	}

	var all []Candidate
	var collectErrs []error
	for _, p := range pruners {
		cs, err := p.Candidates(ctx)
		if err != nil {
			collectErrs = append(collectErrs, fmt.Errorf("%s: %w", p.Name(), err))
			continue
		}
		for i := range cs {
			cs[i].Category = p.Name()
		}
		all = append(all, cs...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		if !all[i].LastUsed.Equal(all[j].LastUsed) {
			return all[i].LastUsed.Before(all[j].LastUsed)
		}
		if all[i].Path != all[j].Path {
			return all[i].Path < all[j].Path
		}
		return all[i].Category < all[j].Category
	})

	want := usedBytes - target
	var freed int64
	for _, c := range all {
		if freed >= want {
			break
		}
		if !apply {
			rep.Removed = append(rep.Removed, c)
			rep.BytesRemoved += c.Bytes
			freed += c.Bytes
			continue
		}
		if c.Delete == nil {
			rep.Skipped = append(rep.Skipped, c)
			continue
		}
		if err := c.Delete(); err != nil {
			skip := c
			skip.Reason = fmt.Sprintf("%s (delete failed: %v)", c.Reason, err)
			rep.Skipped = append(rep.Skipped, skip)
			continue
		}
		rep.Removed = append(rep.Removed, c)
		rep.BytesRemoved += c.Bytes
		freed += c.Bytes
	}
	rep.UsedAfter = usedBytes - rep.BytesRemoved
	return rep, errors.Join(collectErrs...)
}

// RenderPruneHuman writes rep as a fixed-width table to w.
func RenderPruneHuman(w io.Writer, rep PruneReport) error {
	mode := "dry-run"
	if rep.Apply {
		mode = "apply"
	}
	if _, err := fmt.Fprintf(w, "storage prune: mode=%s target=%s used-before=%s used-after=%s\n",
		mode, formatBytes(rep.Target), formatBytes(rep.UsedBefore), formatBytes(rep.UsedAfter)); err != nil {
		return err
	}
	if len(rep.Removed) == 0 && len(rep.Skipped) == 0 {
		_, err := fmt.Fprintln(w, "  (no candidates selected — usage already at or below target)")
		return err
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	verb := "would-remove"
	if rep.Apply {
		verb = "removed"
	}
	for _, c := range rep.Removed {
		if _, err := fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n", verb, c.Category, c.Reason, formatBytes(c.Bytes), c.Path); err != nil {
			return err
		}
	}
	for _, c := range rep.Skipped {
		if _, err := fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n", "skipped", c.Category, c.Reason, formatBytes(c.Bytes), c.Path); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "%s: %s across %d items\n", verb, formatBytes(rep.BytesRemoved), len(rep.Removed)); err != nil {
		return err
	}
	if !rep.Apply && rep.BytesRemoved > 0 {
		_, err := fmt.Fprintln(w, "(re-run with -apply to delete)")
		return err
	}
	return nil
}
