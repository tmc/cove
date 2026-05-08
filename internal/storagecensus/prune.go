package storagecensus

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
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

// PinChecker reports whether a given typed object is pinned by the
// operator and so must be excluded from eviction. The category names
// match the storagepins namespace ("vm", "image", "run", "cache");
// the id is the candidate's stable last-path-segment identifier.
//
// internal/storagepins.File satisfies this interface.
type PinChecker interface {
	IsPinned(category, id string) bool
}

// filterPinned returns cs minus any candidates pinned in pins; *skipped
// is incremented for each filtered entry. The candidate id is the last
// path segment of Candidate.Path, matching the convention storagepins
// uses for vm/image/run/cache refs.
func filterPinned(cs []Candidate, namespace string, pins PinChecker, skipped *int) []Candidate {
	out := cs[:0]
	for _, c := range cs {
		if pins.IsPinned(namespace, filepath.Base(c.Path)) {
			*skipped++
			continue
		}
		out = append(out, c)
	}
	return out
}

// pinNamespace maps a Pruner.Name (e.g. "vms") to the storagepins
// namespace ("vm"). A category absent from the map is not pinnable
// today (e.g. "build-scratch", "store") and its candidates are passed
// through unchanged.
func pinNamespace(prunerName string) string {
	switch prunerName {
	case "vms":
		return "vm"
	case "images":
		return "image"
	case "runs":
		return "run"
	case "cache":
		return "cache"
	}
	return ""
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
	// PinnedSkipped counts candidates excluded because the operator
	// pinned them. The pinned items themselves are not stored on the
	// report; their identity is already the operator's intent.
	PinnedSkipped int
}

// CoordinatePrune walks pruners and selects the oldest candidates across
// all categories until enough bytes are reclaimed to drop usedBytes below
// target. With apply=false the report describes the plan and nothing is
// removed. The selection is stable: ties on LastUsed break by Path then
// Category so two runs against the same on-disk state pick the same set.
//
// When pins is non-nil, candidates whose canonical "namespace:id" matches
// a pin are dropped before sorting and counted in PruneReport.PinnedSkipped.
// Categories without a pin namespace (build-scratch, store) are not checked.
func CoordinatePrune(ctx context.Context, pruners []Pruner, usedBytes, target int64, apply bool, pins PinChecker) (PruneReport, error) {
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
		ns := pinNamespace(p.Name())
		for i := range cs {
			cs[i].Category = p.Name()
		}
		if pins != nil && ns != "" {
			cs = filterPinned(cs, ns, pins, &rep.PinnedSkipped)
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
	if rep.PinnedSkipped > 0 {
		if _, err := fmt.Fprintf(w, "pinned-skipped: %d items\n", rep.PinnedSkipped); err != nil {
			return err
		}
	}
	if !rep.Apply && rep.BytesRemoved > 0 {
		_, err := fmt.Fprintln(w, "(re-run with -apply to delete)")
		return err
	}
	return nil
}
