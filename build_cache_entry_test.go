package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tmc/vz-macos/internal/store"
)

func TestBuildCacheEntryRoundTrip(t *testing.T) {
	s := store.New(t.TempDir())
	key := digestBytes([]byte("key"))
	layer := digestBytes([]byte("layer"))
	created := time.Date(2026, 4, 29, 1, 2, 3, 0, time.UTC)
	entry := buildCacheEntry{Key: key, ParentDigest: digestBytes([]byte("parent")), ScriptDigest: digestBytes([]byte("script")), AgentProtocolVersion: "1", Compact: "targeted", LayerDigest: layer, CreatedAt: created}
	if err := saveBuildCacheEntry(s, entry); err != nil {
		t.Fatalf("saveBuildCacheEntry(): %v", err)
	}
	got, err := loadBuildCacheEntry(s, key)
	if err != nil {
		t.Fatalf("loadBuildCacheEntry(): %v", err)
	}
	if got != entry {
		t.Fatalf("entry = %#v, want %#v", got, entry)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "build-cache", "keys", digestFileName(key)+".json")); err != nil {
		t.Fatalf("stat cache key: %v", err)
	}
}

func TestBuildLayerManifestRoundTrip(t *testing.T) {
	s := store.New(t.TempDir())
	manifest := buildLayerManifest{Digest: digestBytes([]byte("manifest")), BlockSize: 65536, DiskSize: 123, Blocks: []buildLayerBlock{{Offset: 0, Size: 4, Digest: digestBytes([]byte("blob"))}}}
	if err := saveBuildLayerManifest(s, manifest); err != nil {
		t.Fatalf("saveBuildLayerManifest(): %v", err)
	}
	got, err := loadBuildLayerManifest(s, manifest.Digest)
	if err != nil {
		t.Fatalf("loadBuildLayerManifest(): %v", err)
	}
	if got.Digest != manifest.Digest || got.BlockSize != manifest.BlockSize || len(got.Blocks) != 1 || got.Blocks[0] != manifest.Blocks[0] {
		t.Fatalf("manifest = %#v, want %#v", got, manifest)
	}
}

func TestLoadBuildCacheEntryRejectsMismatchedKey(t *testing.T) {
	s := store.New(t.TempDir())
	key := digestBytes([]byte("key"))
	other := digestBytes([]byte("other"))
	path := filepath.Join(s.Dir, "build-cache", "keys", digestFileName(key)+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"key":"`+other+`"}`), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := loadBuildCacheEntry(s, key)
	if err == nil {
		t.Fatal("loadBuildCacheEntry() error = nil, want mismatch")
	}
}

func TestLoadBuildCacheEntryMissing(t *testing.T) {
	s := store.New(t.TempDir())
	_, err := loadBuildCacheEntry(s, digestBytes([]byte("missing")))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error = %v, want os.ErrNotExist", err)
	}
}
