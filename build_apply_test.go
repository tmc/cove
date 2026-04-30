package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/vz-macos/internal/store"
)

func TestApplyCacheHitMaterializesDisk(t *testing.T) {
	parent, want, step, exec := makeCacheHitFixture(t, false)
	result, err := exec.applyCacheHit(context.Background(), step, parent)
	if err != nil {
		t.Fatalf("applyCacheHit(): %v", err)
	}
	got, err := os.ReadFile(result.DiskPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("scratch disk = %q, want %q", got, want)
	}
	if result.Step != step.Name || result.Key != step.Key || result.LayerDigest == "" {
		t.Fatalf("result = %#v", result)
	}
}

func TestApplyCacheHitValidatesEntryBeforeScratch(t *testing.T) {
	exec := testBuildExecutor(t.TempDir())
	_, err := exec.applyCacheHit(context.Background(), buildPlanStep{Name: "bad", Key: "not-a-digest"}, "parent.img")
	if err == nil {
		t.Fatal("applyCacheHit() error = nil, want invalid key")
	}
	assertEmptyDir(t, exec.scratchRoot)
}

func TestApplyCacheHitValidatesLayerBeforeScratch(t *testing.T) {
	root := t.TempDir()
	exec := testBuildExecutor(filepath.Join(root, "scratch"))
	exec.store = store.New(filepath.Join(root, "store"))
	key := "sha256:" + strings.Repeat("a", 64)
	if err := saveBuildCacheEntry(exec.store, buildCacheEntry{Key: key, LayerDigest: "sha256:" + strings.Repeat("b", 64)}); err != nil {
		t.Fatal(err)
	}
	_, err := exec.applyCacheHit(context.Background(), buildPlanStep{Name: "missing", Key: key}, "parent.img")
	if err == nil {
		t.Fatal("applyCacheHit() error = nil, want missing layer")
	}
	assertEmptyDir(t, exec.scratchRoot)
}

func TestApplyCacheHitValidatesBlocksBeforeScratch(t *testing.T) {
	root := t.TempDir()
	exec := testBuildExecutor(filepath.Join(root, "scratch"))
	exec.store = store.New(filepath.Join(root, "store"))
	key := "sha256:" + strings.Repeat("a", 64)
	layer := "sha256:" + strings.Repeat("b", 64)
	manifest := buildLayerManifest{
		Digest:    layer,
		BlockSize: buildDeltaBlockSize,
		DiskSize:  4,
		Blocks: []buildLayerBlock{{
			Offset: 0,
			Size:   4,
			Digest: "sha256:" + strings.Repeat("c", 64),
		}},
	}
	if err := saveBuildLayerManifest(exec.store, manifest); err != nil {
		t.Fatal(err)
	}
	if err := saveBuildCacheEntry(exec.store, buildCacheEntry{Key: key, LayerDigest: layer}); err != nil {
		t.Fatal(err)
	}
	_, err := exec.applyCacheHit(context.Background(), buildPlanStep{Name: "missing-block", Key: key}, "parent.img")
	if err == nil {
		t.Fatal("applyCacheHit() error = nil, want missing block")
	}
	assertEmptyDir(t, exec.scratchRoot)
}

func TestApplyCacheHitFailureCleansScratch(t *testing.T) {
	_, _, step, exec := makeCacheHitFixture(t, false)
	_, err := exec.applyCacheHit(context.Background(), step, filepath.Join(t.TempDir(), "missing.img"))
	if err == nil {
		t.Fatal("applyCacheHit() error = nil, want missing parent")
	}
	assertEmptyDir(t, exec.scratchRoot)
}

func TestApplyCacheHitFailureKeepsScratch(t *testing.T) {
	_, _, step, exec := makeCacheHitFixture(t, true)
	_, err := exec.applyCacheHit(context.Background(), step, filepath.Join(t.TempDir(), "missing.img"))
	if err == nil {
		t.Fatal("applyCacheHit() error = nil, want missing parent")
	}
	entries, readErr := os.ReadDir(exec.scratchRoot)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 1 {
		t.Fatalf("scratch entries = %d, want 1", len(entries))
	}
}

func makeCacheHitFixture(t *testing.T, keep bool) (parent string, want []byte, step buildPlanStep, exec *buildExecutor) {
	t.Helper()
	root := t.TempDir()
	parent = filepath.Join(root, "parent.img")
	if err := os.WriteFile(parent, []byte("hello parent\n"), 0644); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(root, "child.img")
	want = []byte("hello cached\n")
	if err := os.WriteFile(child, want, 0644); err != nil {
		t.Fatal(err)
	}
	delta, err := DiffDisks(parent, child)
	if err != nil {
		t.Fatal(err)
	}
	exec = testBuildExecutor(filepath.Join(root, "scratch"))
	exec.opts.KeepIntermediate = keep
	exec.store = store.New(filepath.Join(root, "store"))
	manifest, err := StoreDiskDelta(exec.store, delta)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveBuildLayerManifest(exec.store, manifest); err != nil {
		t.Fatal(err)
	}
	key := "sha256:" + strings.Repeat("a", 64)
	if err := saveBuildCacheEntry(exec.store, buildCacheEntry{Key: key, LayerDigest: manifest.Digest}); err != nil {
		t.Fatal(err)
	}
	step = buildPlanStep{Name: "cached", Key: key, CacheHit: true, LayerDigest: manifest.Digest}
	return parent, want, step, exec
}

func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("%s has %d entries, want empty", dir, len(entries))
	}
}
