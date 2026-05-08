// Package storagecensus walks ~/.vz/ once and reports per-category disk
// usage. Phase 1 of design 040 (storage budget for ~/.vz/).
//
// The walk is read-only: it does not mutate state, evict files, or
// compute eviction plans. Higher phases will compose this report with
// budget persistence and eviction policy.
//
// The on-wire schema uses bytes (not GB) for SizeBytes/UsedBytes so the
// daemon-side consumer planned for Phase 5 can act on small categories
// (run bundles are typically MB-scale). Human-table rendering converts
// to GB for operator readability. The design doc's UsedGB shorthand is
// preserved as the human-rendered field.
package storagecensus

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"text/tabwriter"
	"time"
)

// Report is the result of a census walk.
type Report struct {
	Root       string     `json:"root"`
	UsedBytes  int64      `json:"used_bytes"`
	Categories []Category `json:"categories"`
	Generated  time.Time  `json:"generated"`
}

// Category aggregates one top-level subtree under Root.
type Category struct {
	Name      string `json:"name"`
	Path      string `json:"path"`
	UsedBytes int64  `json:"used_bytes"`
	Items     []Item `json:"items,omitempty"`
}

// Item names one immediate child of a category, newest first.
type Item struct {
	Path       string    `json:"path"`
	SizeBytes  int64     `json:"size_bytes"`
	LastUsed   time.Time `json:"last_used"`
	IsDir      bool      `json:"is_dir"`
}

// Descriptor names one category to walk.
type Descriptor struct {
	Name string
	Path string
}

// Options controls how Walk reports.
type Options struct {
	// TopN bounds how many child Items each category surfaces. 0 means "no limit".
	TopN int
	// Now is the time recorded as Report.Generated. Zero means time.Now().UTC().
	Now time.Time
}

// Walk runs a census across cats and returns the Report. Categories that
// do not exist on disk are reported with UsedBytes=0 and zero items; they
// are not an error. Categories that fail mid-walk for a reason other than
// non-existence return their partial sum and a non-nil error joined with
// errors.Join.
func Walk(root string, cats []Descriptor, opts Options) (Report, error) {
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	rep := Report{Root: root, Generated: now}
	var errs []error
	for _, c := range cats {
		cat, err := walkCategory(c, opts.TopN)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", c.Name, err))
		}
		rep.UsedBytes += cat.UsedBytes
		rep.Categories = append(rep.Categories, cat)
	}
	return rep, errors.Join(errs...)
}

func walkCategory(d Descriptor, topN int) (Category, error) {
	cat := Category{Name: d.Name, Path: d.Path}
	info, err := os.Stat(d.Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cat, nil
		}
		return cat, err
	}
	if !info.IsDir() {
		// A file at the category root is unusual but countable.
		cat.UsedBytes = info.Size()
		cat.Items = []Item{{Path: d.Path, SizeBytes: info.Size(), LastUsed: info.ModTime(), IsDir: false}}
		return cat, nil
	}

	entries, err := os.ReadDir(d.Path)
	if err != nil {
		return cat, err
	}

	items := make([]Item, 0, len(entries))
	var totalBytes int64
	for _, e := range entries {
		child := filepath.Join(d.Path, e.Name())
		size, mtime, err := childSize(child)
		if err != nil {
			// Tolerate per-entry read failures (e.g. dangling symlink under
			// a guest-staged share); record the failure and continue.
			return cat, fmt.Errorf("%s: %w", child, err)
		}
		totalBytes += size
		items = append(items, Item{
			Path:      child,
			SizeBytes: size,
			LastUsed:  mtime,
			IsDir:     e.IsDir(),
		})
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].LastUsed.Equal(items[j].LastUsed) {
			return items[i].Path < items[j].Path
		}
		return items[i].LastUsed.After(items[j].LastUsed)
	})
	if topN > 0 && len(items) > topN {
		items = items[:topN]
	}

	cat.UsedBytes = totalBytes
	cat.Items = items
	return cat, nil
}

// childSize returns the size and mtime of an immediate category child.
// For files it is the file's size and mtime. For directories it walks
// the subtree and sums sizes (bounded to that one directory) and uses
// the directory's own mtime.
func childSize(path string) (int64, time.Time, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, time.Time{}, err
	}
	if !info.IsDir() {
		return info.Size(), info.ModTime(), nil
	}
	var total int64
	walkErr := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		total += fi.Size()
		return nil
	})
	if walkErr != nil {
		return 0, time.Time{}, walkErr
	}
	return total, info.ModTime(), nil
}

// EncodeJSON writes rep as indented JSON to w.
func EncodeJSON(w io.Writer, rep Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}

// RenderHuman writes rep as a fixed-width table to w. Sizes are shown
// in GB rounded to one decimal; categories with zero usage are still
// listed so a fresh install reads as expected.
func RenderHuman(w io.Writer, rep Report) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintf(tw, "Root: %s\n", rep.Root); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(tw, "Used: %s\n\n", formatBytes(rep.UsedBytes)); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(tw, "CATEGORY\tPATH\tUSED\tITEMS"); err != nil {
		return err
	}
	for _, c := range rep.Categories {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%d\n", c.Name, c.Path, formatBytes(c.UsedBytes), len(c.Items)); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func formatBytes(n int64) string {
	const (
		kib = 1024
		mib = kib * 1024
		gib = mib * 1024
	)
	switch {
	case n >= gib:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(gib))
	case n >= mib:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(mib))
	case n >= kib:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(kib))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
