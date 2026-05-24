package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tmc/cove/internal/imagestore"
)

// TestImageCreatedAt covers the three branches of imageCreatedAt:
// manifest-with-timestamp, stat-fallback, and the now() fallback.
func TestImageCreatedAt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	want := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	ref := ImageRef{Name: "test", Tag: "v1"}

	t.Run("manifest timestamp wins", func(t *testing.T) {
		entry := imagestore.Entry{Ref: ref, Manifest: &imagestore.Manifest{CreatedAt: want}}
		got := imageCreatedAt(entry)
		if !got.Equal(want) {
			t.Errorf("imageCreatedAt = %v, want %v", got, want)
		}
	})

	t.Run("manifest stat fallback", func(t *testing.T) {
		dir := ref.Path()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "manifest.json")
		if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, want, want); err != nil {
			t.Fatal(err)
		}
		entry := imagestore.Entry{Ref: ref, Manifest: &imagestore.Manifest{}} // zero CreatedAt
		got := imageCreatedAt(entry)
		if !got.Equal(want) {
			t.Errorf("imageCreatedAt = %v, want %v", got, want)
		}
	})

	t.Run("now fallback when nothing on disk", func(t *testing.T) {
		entry := imagestore.Entry{Ref: ImageRef{Name: "missing", Tag: "v1"}}
		before := time.Now()
		got := imageCreatedAt(entry)
		after := time.Now()
		if got.Before(before) || got.After(after) {
			t.Errorf("imageCreatedAt = %v, want between %v and %v", got, before, after)
		}
	})

	// ensure ImageManifest round-trips with a CreatedAt so future readers
	// of this test see the JSON shape that exercises the manifest branch.
	if _, err := json.Marshal(imagestore.Manifest{CreatedAt: want}); err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
}
