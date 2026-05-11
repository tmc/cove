package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestBuildCacheEntryCreatesPrivateDirs(t *testing.T) {
	s := store.New(t.TempDir())
	key := digestBytes([]byte("key"))
	entry := buildCacheEntry{
		Key:                  key,
		ParentDigest:         digestBytes([]byte("parent")),
		ScriptDigest:         digestBytes([]byte("script")),
		AgentProtocolVersion: agentProtocolVersion,
		Compact:              "targeted",
		LayerDigest:          digestBytes([]byte("layer")),
	}
	if err := saveBuildCacheEntry(s, entry); err != nil {
		t.Fatalf("saveBuildCacheEntry(): %v", err)
	}
	for _, dir := range []string{
		filepath.Join(s.Dir, "build-cache"),
		filepath.Join(s.Dir, "build-cache", "keys"),
	} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat %s: %v", dir, err)
		}
		if got := info.Mode().Perm(); got != 0700 {
			t.Fatalf("%s mode = %o, want 0700", dir, got)
		}
	}
}

func TestBuildLayerManifestRoundTrip(t *testing.T) {
	s := store.New(t.TempDir())
	manifest := buildLayerManifest{BlockSize: 65536, DiskSize: 123, Blocks: []buildLayerBlock{{Offset: 0, Size: 4, Digest: digestBytes([]byte("blob"))}}}
	digest, err := digestBuildLayerManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Digest = digest
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

func TestSaveBuildCacheEntryRejectsInvalidLayerDigest(t *testing.T) {
	s := store.New(t.TempDir())
	entry := buildCacheEntry{
		Key:                  digestBytes([]byte("key")),
		ParentDigest:         digestBytes([]byte("parent")),
		ScriptDigest:         digestBytes([]byte("script")),
		AgentProtocolVersion: agentProtocolVersion,
		Compact:              "targeted",
		LayerDigest:          "sha256:not-a-real-digest",
	}
	if err := saveBuildCacheEntry(s, entry); err == nil {
		t.Fatal("saveBuildCacheEntry() error = nil, want invalid layer digest")
	}
}

func TestSaveBuildCacheEntryRejectsMissingMetadata(t *testing.T) {
	s := store.New(t.TempDir())
	entry := buildCacheEntry{
		Key:         digestBytes([]byte("key")),
		LayerDigest: digestBytes([]byte("layer")),
	}
	if err := saveBuildCacheEntry(s, entry); err == nil {
		t.Fatal("saveBuildCacheEntry() error = nil, want missing metadata")
	}
}

func TestLoadBuildCacheEntryRejectsInvalidLayerDigest(t *testing.T) {
	s := store.New(t.TempDir())
	key := digestBytes([]byte("key"))
	path := filepath.Join(s.Dir, "build-cache", "keys", digestFileName(key)+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"key":"`+key+`","layer_digest":"sha256:not-a-real-digest"}`), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := loadBuildCacheEntry(s, key)
	if err == nil {
		t.Fatal("loadBuildCacheEntry() error = nil, want invalid layer digest")
	}
}

func TestSaveBuildLayerManifestRejectsInvalidDigest(t *testing.T) {
	s := store.New(t.TempDir())
	manifest := buildLayerManifest{
		Digest:    "sha256:not-a-real-digest",
		BlockSize: 65536,
		DiskSize:  123,
	}
	if err := saveBuildLayerManifest(s, manifest); err == nil {
		t.Fatal("saveBuildLayerManifest() error = nil, want invalid digest")
	}
}

func TestSaveBuildLayerManifestRejectsDigestMismatch(t *testing.T) {
	s := store.New(t.TempDir())
	manifest := buildLayerManifest{
		Digest:    digestBytes([]byte("manifest")),
		BlockSize: 65536,
		DiskSize:  123,
		Blocks:    []buildLayerBlock{{Offset: 0, Size: 4, Digest: digestBytes([]byte("blob"))}},
	}
	if err := saveBuildLayerManifest(s, manifest); err == nil {
		t.Fatal("saveBuildLayerManifest() error = nil, want digest mismatch")
	}
}

func TestSaveBuildLayerManifestRejectsInvalidBlockRange(t *testing.T) {
	s := store.New(t.TempDir())
	tests := []struct {
		name  string
		block buildLayerBlock
		want  string
	}{
		{
			name:  "oversized",
			block: buildLayerBlock{Offset: 0, Size: 5, Digest: digestBytes([]byte("blob"))},
			want:  "exceeds block size",
		},
		{
			name:  "unaligned",
			block: buildLayerBlock{Offset: 2, Size: 1, Digest: digestBytes([]byte("blob"))},
			want:  "unaligned offset",
		},
		{
			name:  "past-disk",
			block: buildLayerBlock{Offset: 4, Size: 4, Digest: digestBytes([]byte("blob"))},
			want:  "range exceeds disk size",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manifest := buildLayerManifest{
				BlockSize: 4,
				DiskSize:  6,
				Blocks:    []buildLayerBlock{tt.block},
			}
			digest, err := digestBuildLayerManifest(manifest)
			if err != nil {
				t.Fatal(err)
			}
			manifest.Digest = digest
			err = saveBuildLayerManifest(s, manifest)
			if err == nil {
				t.Fatal("saveBuildLayerManifest() error = nil, want invalid block range")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("saveBuildLayerManifest() = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestLoadBuildLayerManifestRejectsInvalidBlockDigest(t *testing.T) {
	s := store.New(t.TempDir())
	digest := digestBytes([]byte("manifest"))
	path := filepath.Join(s.Dir, "build-cache", "layers", digestFileName(digest)+".json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{
		"digest":"` + digest + `",
		"block_size":65536,
		"disk_size":123,
		"blocks":[{"offset":0,"size":4,"digest":"sha256:not-a-real-digest"}]
	}`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	_, err := loadBuildLayerManifest(s, digest)
	if err == nil {
		t.Fatal("loadBuildLayerManifest() error = nil, want invalid block digest")
	}
}

func TestLoadBuildLayerManifestRejectsDigestMismatch(t *testing.T) {
	s := store.New(t.TempDir())
	manifest := buildLayerManifest{
		BlockSize: 65536,
		DiskSize:  123,
		Blocks:    []buildLayerBlock{{Offset: 0, Size: 4, Digest: digestBytes([]byte("blob"))}},
	}
	digest, err := digestBuildLayerManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Digest = digest
	manifest.DiskSize = 456
	path := filepath.Join(s.Dir, "build-cache", "layers", digestFileName(digest)+".json")
	if err := writeBuildCacheJSON(path, manifest); err != nil {
		t.Fatal(err)
	}
	_, err = loadBuildLayerManifest(s, digest)
	if err == nil {
		t.Fatal("loadBuildLayerManifest() error = nil, want digest mismatch")
	}
}

func TestLoadBuildCacheEntryMissing(t *testing.T) {
	s := store.New(t.TempDir())
	_, err := loadBuildCacheEntry(s, digestBytes([]byte("missing")))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("error = %v, want os.ErrNotExist", err)
	}
}

func testBuildPlanStep(name, key string) buildPlanStep {
	return buildPlanStep{
		Name:                 name,
		Key:                  key,
		ParentDigest:         digestBytes([]byte(name + "-parent")),
		ScriptDigest:         digestBytes([]byte(name + "-script")),
		AgentProtocolVersion: agentProtocolVersion,
		Meta:                 buildScriptMeta{Compact: "targeted"},
	}
}

func testCacheEntryForStep(step buildPlanStep, layer string) buildCacheEntry {
	return buildCacheEntry{
		Key:                  step.Key,
		ParentDigest:         step.ParentDigest,
		ScriptDigest:         step.ScriptDigest,
		AgentProtocolVersion: step.AgentProtocolVersion,
		Compact:              step.Meta.Compact,
		LayerDigest:          layer,
	}
}
