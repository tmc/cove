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
	"math"
	"os"
	"path/filepath"
	"reflect"
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
	// Budget is the persisted storage budget, when one is configured.
	// Nil means no budget on disk; the zero value never appears here.
	Budget *Budget `json:"budget,omitempty"`
}

// State classifies a Report's UsedBytes against its Budget tripwires.
type State int

const (
	// StateUnset indicates no budget is configured.
	StateUnset State = iota
	// StateOK means usage is below all configured tripwires.
	StateOK
	// StateWarn means usage has crossed warn_pct of the target.
	StateWarn
	// StateHard means usage has crossed hard_pct of the target.
	StateHard
)

// String returns a short label for the state.
func (s State) String() string {
	switch s {
	case StateUnset:
		return "unset"
	case StateOK:
		return "ok"
	case StateWarn:
		return "warn"
	case StateHard:
		return "hard"
	default:
		return fmt.Sprintf("state(%d)", int(s))
	}
}

// State returns the current tripwire state for rep, given its Budget.
// StateUnset is returned when Budget is nil or the budget is the zero
// value.
func (rep Report) State() State {
	if rep.Budget == nil || !rep.Budget.IsSet() {
		return StateUnset
	}
	if hard := rep.Budget.HardBytes(); hard > 0 && rep.UsedBytes >= hard {
		return StateHard
	}
	if warn := rep.Budget.WarnBytes(); warn > 0 && rep.UsedBytes >= warn {
		return StateWarn
	}
	return StateOK
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
	Path      string    `json:"path"`
	SizeBytes int64     `json:"size_bytes"`
	LastUsed  time.Time `json:"last_used"`
	IsDir     bool      `json:"is_dir"`
	// Pinned reports whether the operator has marked this item to be
	// kept across budget enforcement. Set by Walk when Options.Pins
	// names the item by canonical "category:id".
	Pinned bool `json:"pinned,omitempty"`
}

// Descriptor names one category to walk.
type Descriptor struct {
	Name string
	Path string
}

// DefaultDescriptors returns the canonical category list under root,
// matching the layout cove writes today. Used by the daemon poll loop
// (design 040 Phase 5) so it does not have to import the package main
// path helpers.
func DefaultDescriptors(root string) []Descriptor {
	return []Descriptor{
		{Name: "vms", Path: filepath.Join(root, "vms")},
		{Name: "images", Path: filepath.Join(root, "images")},
		{Name: "runs", Path: filepath.Join(root, "runs")},
		{Name: "cache", Path: filepath.Join(root, "cache")},
		{Name: "build-scratch", Path: filepath.Join(root, "build-scratch")},
		{Name: "store", Path: filepath.Join(root, "store")},
	}
}

// Options controls how Walk reports.
type Options struct {
	// TopN bounds how many child Items each category surfaces. 0 means "no limit".
	TopN int
	// Now is the time recorded as Report.Generated. Zero means time.Now().UTC().
	Now time.Time
	// Pins, if set, marks Items whose canonical "category:id" appears
	// as a key. CategoryToPinName maps a Walk category name (e.g. "vms")
	// to the pin namespace ("vm"). Walk derives the Item's id from the
	// last path element of Item.Path.
	Pins map[string]bool
	// CategoryToPinName maps a Walk category name to the pin namespace
	// used when looking up Pins. A category not in the map is not
	// considered for pinning. The zero value yields no pinning.
	CategoryToPinName map[string]string
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
		// Walk every category in full first; trimming and pin marking
		// happen after so pinned items survive a TopN cut.
		cat, err := walkCategory(c, 0)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", c.Name, err))
		}
		if pinName, ok := opts.CategoryToPinName[c.Name]; ok && len(opts.Pins) > 0 {
			for i := range cat.Items {
				id := filepath.Base(cat.Items[i].Path)
				if opts.Pins[pinName+":"+id] {
					cat.Items[i].Pinned = true
				}
			}
		}
		if opts.TopN > 0 && len(cat.Items) > opts.TopN {
			head := cat.Items[:opts.TopN]
			var pinnedTail []Item
			for _, it := range cat.Items[opts.TopN:] {
				if it.Pinned {
					pinnedTail = append(pinnedTail, it)
				}
			}
			cat.Items = append(head, pinnedTail...)
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
		size := fileUsageBytes(info)
		cat.UsedBytes = size
		cat.Items = []Item{{Path: d.Path, SizeBytes: size, LastUsed: info.ModTime(), IsDir: false}}
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
		return fileUsageBytes(info), info.ModTime(), nil
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
		total += fileUsageBytes(fi)
		return nil
	})
	if walkErr != nil {
		return 0, time.Time{}, walkErr
	}
	return total, info.ModTime(), nil
}

func fileUsageBytes(info fs.FileInfo) int64 {
	if blocks, ok := statBlocks(info.Sys()); ok && blocks <= math.MaxInt64/512 {
		return blocks * 512
	}
	return info.Size()
}

func statBlocks(sys any) (int64, bool) {
	v := reflect.ValueOf(sys)
	if !v.IsValid() {
		return 0, false
	}
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return 0, false
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return 0, false
	}
	field := v.FieldByName("Blocks")
	if !field.IsValid() || !field.CanInt() {
		return 0, false
	}
	blocks := field.Int()
	if blocks < 0 {
		return 0, false
	}
	return blocks, true
}

// EncodeJSON writes rep as indented JSON to w.
func EncodeJSON(w io.Writer, rep Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}

// RenderHuman writes rep as a fixed-width table to w. Sizes are shown
// in GB rounded to one decimal; categories with zero usage are still
// listed so a fresh install reads as expected. When rep.Budget is
// configured the header also surfaces the target, headroom, and
// tripwire state.
func RenderHuman(w io.Writer, rep Report) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintf(tw, "Root: %s\n", rep.Root); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(tw, "Used: %s\n", formatBytes(rep.UsedBytes)); err != nil {
		return err
	}
	if rep.Budget != nil && rep.Budget.IsSet() {
		headroom := rep.Budget.TargetBytes - rep.UsedBytes
		marker := ""
		switch rep.State() {
		case StateWarn:
			marker = " [WARN]"
		case StateHard:
			marker = " [HARD]"
		}
		if _, err := fmt.Fprintf(tw, "Target: %s\n", formatBytes(rep.Budget.TargetBytes)); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(tw, "Headroom: %s%s\n", formatBytes(headroom), marker); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(tw); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(tw, "CATEGORY\tPATH\tUSED\tITEMS"); err != nil {
		return err
	}
	for _, c := range rep.Categories {
		pinned := 0
		for _, it := range c.Items {
			if it.Pinned {
				pinned++
			}
		}
		row := fmt.Sprintf("%s\t%s\t%s\t%d", c.Name, c.Path, formatBytes(c.UsedBytes), len(c.Items))
		if pinned > 0 {
			row += fmt.Sprintf(" (★%d)", pinned)
		}
		if _, err := fmt.Fprintln(tw, row); err != nil {
			return err
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	return renderPinnedBlock(w, rep)
}

// renderPinnedBlock writes a "Pinned:" footer listing the pinned items
// across all categories. It is a no-op when no items are pinned.
func renderPinnedBlock(w io.Writer, rep Report) error {
	type entry struct {
		ref  string
		path string
	}
	var entries []entry
	for _, c := range rep.Categories {
		for _, it := range c.Items {
			if !it.Pinned {
				continue
			}
			id := filepath.Base(it.Path)
			pinName := categoryToPinNameForRender(c.Name)
			if pinName == "" {
				continue
			}
			entries = append(entries, entry{ref: pinName + ":" + id, path: it.Path})
		}
	}
	if len(entries) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(w); err != nil {
		return err
	}
	if _, err := fmt.Fprintln(w, "Pinned:"); err != nil {
		return err
	}
	for _, e := range entries {
		if _, err := fmt.Fprintf(w, "  ★ %s (%s)\n", e.ref, e.path); err != nil {
			return err
		}
	}
	return nil
}

// categoryToPinNameForRender mirrors the conventional Walk mapping for
// the human renderer. Walk supplies the same mapping via Options when a
// caller wants Pinned flags set; the renderer needs it again to recover
// the canonical "category:id" string from an Item.Path. Categories that
// have no pin namespace (e.g. "build-scratch") return "".
func categoryToPinNameForRender(category string) string {
	switch category {
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

func formatBytes(n int64) string {
	if n < 0 {
		return "-" + formatBytes(-n)
	}
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
