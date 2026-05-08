// Package storagepins persists operator-supplied "keep this" markers
// for objects under ~/.vz/. A pinned object is exempt from the storage
// budget's eviction loop.
//
// Pins are referenced by typed identifiers of the form
// "category:id". Recognized categories:
//
//	vm:<name>          a VM bundle under ~/.vz/vms/<name>/
//	image:<ref>        a local image entry under ~/.vz/images/<ref>/
//	run:<id>           a run bundle under ~/.vz/runs/<id>/
//	cache:<sha>        a content-addressed cache entry
//
// The on-disk file is ~/.vz/pins.json. Save writes via tempfile + rename
// so concurrent readers always see either the previous or the new state.
//
// Phase 3 of design 040 (storage budget for ~/.vz/).
package storagepins

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Filename is the basename of the pins file under the cove root.
const Filename = "pins.json"

// Categories enumerates the recognized pin object categories.
var Categories = []string{"vm", "image", "run", "cache"}

// Pin records a single operator-pinned object.
type Pin struct {
	Category string    `json:"category"`
	ID       string    `json:"id"`
	AddedAt  time.Time `json:"added_at"`
}

// Ref returns the canonical "category:id" form.
func (p Pin) Ref() string { return p.Category + ":" + p.ID }

// File is the on-disk pin set. The zero value is a valid empty set.
type File struct {
	mu   sync.Mutex
	pins map[string]Pin // key is canonical "category:id"
}

// New returns an empty File.
func New() *File { return &File{pins: map[string]Pin{}} }

// ParseRef splits an operator-supplied "category:id" reference into its
// parts and validates both. Whitespace around the reference is rejected:
// the operator's input is taken verbatim.
func ParseRef(ref string) (category, id string, err error) {
	i := strings.IndexByte(ref, ':')
	if i <= 0 || i == len(ref)-1 {
		return "", "", fmt.Errorf("pin ref %q must be of the form category:id", ref)
	}
	category = ref[:i]
	id = ref[i+1:]
	if !validCategory(category) {
		return "", "", fmt.Errorf("pin category %q must be one of %s", category, strings.Join(Categories, ", "))
	}
	if strings.ContainsAny(id, " \t\n\r") {
		return "", "", fmt.Errorf("pin id %q must not contain whitespace", id)
	}
	if id == "." || id == ".." || strings.Contains(id, "/") {
		// Reject path-like ids so a future code path that materializes
		// a pin into a filesystem path cannot escape its category root.
		return "", "", fmt.Errorf("pin id %q must not contain path separators", id)
	}
	return category, id, nil
}

func validCategory(c string) bool {
	for _, ok := range Categories {
		if c == ok {
			return true
		}
	}
	return false
}

// Add records a pin. Adding an already-pinned ref is a no-op (the
// AddedAt timestamp is preserved).
func (f *File) Add(category, id string, now time.Time) error {
	if !validCategory(category) {
		return fmt.Errorf("pin category %q must be one of %s", category, strings.Join(Categories, ", "))
	}
	if id == "" {
		return errors.New("pin id must not be empty")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pins == nil {
		f.pins = map[string]Pin{}
	}
	key := category + ":" + id
	if _, ok := f.pins[key]; ok {
		return nil
	}
	f.pins[key] = Pin{Category: category, ID: id, AddedAt: now.UTC()}
	return nil
}

// Remove drops a pin by canonical ref. Removing a missing pin returns
// false and a nil error.
func (f *File) Remove(category, id string) (bool, error) {
	if !validCategory(category) {
		return false, fmt.Errorf("pin category %q must be one of %s", category, strings.Join(Categories, ", "))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	key := category + ":" + id
	if _, ok := f.pins[key]; !ok {
		return false, nil
	}
	delete(f.pins, key)
	return true, nil
}

// IsPinned reports whether the typed object is pinned.
func (f *File) IsPinned(category, id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.pins[category+":"+id]
	return ok
}

// List returns the pin set sorted by category then id.
func (f *File) List() []Pin {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Pin, 0, len(f.pins))
	for _, p := range f.pins {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// RefSet returns a map of canonical "category:id" to true for every pin.
// Useful for o(1) lookup from a single Walk pass.
func (f *File) RefSet() map[string]bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]bool, len(f.pins))
	for k := range f.pins {
		out[k] = true
	}
	return out
}

type onDisk struct {
	Pins []Pin `json:"pins"`
}

// Load reads ~/.vz/pins.json from root. A missing file yields an empty
// File and a nil error so callers can treat "no pins" the same as
// "pins file not yet written".
func Load(root string) (*File, error) {
	path := filepath.Join(root, Filename)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return New(), nil
		}
		return nil, fmt.Errorf("read pins: %w", err)
	}
	var d onDisk
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("parse pins: %w", err)
	}
	f := New()
	for _, p := range d.Pins {
		if !validCategory(p.Category) || p.ID == "" {
			// A bad on-disk record is dropped silently rather than
			// failing the whole load: the operator may have hand-edited
			// the file. Save will rewrite the canonical form.
			continue
		}
		f.pins[p.Ref()] = p
	}
	return f, nil
}

// Save writes f to ~/.vz/pins.json under root. The cove root is created
// if missing. The write is atomic: a sibling tempfile is created and
// renamed into place after fsync.
func Save(root string, f *File) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("ensure cove root: %w", err)
	}
	pins := f.List()
	d := onDisk{Pins: pins}
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return fmt.Errorf("encode pins: %w", err)
	}
	path := filepath.Join(root, Filename)
	tmp, err := os.CreateTemp(root, ".pins.json.")
	if err != nil {
		return fmt.Errorf("create pins tempfile: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { os.Remove(tmpName) }
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("write pins tempfile: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("fsync pins tempfile: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close pins tempfile: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("rename pins tempfile: %w", err)
	}
	return nil
}
