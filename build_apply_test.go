package main

import (
	"bytes"
	"context"
	"errors"
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

func TestExecuteCacheHitsChainsLayers(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent.img")
	if err := os.WriteFile(parent, []byte("base image\n"), 0644); err != nil {
		t.Fatal(err)
	}
	storeDir := filepath.Join(root, "store")
	s := store.New(storeDir)
	step1 := storeCacheLayer(t, s, "sha256:"+strings.Repeat("1", 64), parent, []byte("layer one\n"))
	step2Parent := filepath.Join(root, "step1.img")
	if err := os.WriteFile(step2Parent, []byte("layer one\n"), 0644); err != nil {
		t.Fatal(err)
	}
	step2 := storeCacheLayer(t, s, "sha256:"+strings.Repeat("2", 64), step2Parent, []byte("layer two\n"))
	exec := testBuildExecutor(filepath.Join(root, "scratch"))
	exec.store = s
	exec.opts.KeepIntermediate = false
	exec.plan.Steps = []buildPlanStep{step1, step2}
	result, err := exec.executeCacheHits(context.Background(), parent)
	if err != nil {
		t.Fatalf("executeCacheHits(): %v", err)
	}
	got, err := os.ReadFile(result.DiskPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "layer two\n" {
		t.Fatalf("final disk = %q, want layer two", got)
	}
	entries, err := os.ReadDir(exec.scratchRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("scratch entries = %d, want final scratch only", len(entries))
	}
}

func TestExecuteCacheHitsStopsAtMissAndCleansScratch(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent.img")
	if err := os.WriteFile(parent, []byte("base image\n"), 0644); err != nil {
		t.Fatal(err)
	}
	s := store.New(filepath.Join(root, "store"))
	hit := storeCacheLayer(t, s, "sha256:"+strings.Repeat("1", 64), parent, []byte("layer one\n"))
	miss := buildPlanStep{Name: "miss", Key: "sha256:" + strings.Repeat("2", 64)}
	exec := testBuildExecutor(filepath.Join(root, "scratch"))
	exec.store = s
	exec.opts.KeepIntermediate = false
	exec.plan.Steps = []buildPlanStep{hit, miss}
	_, err := exec.executeCacheHits(context.Background(), parent)
	if !errors.Is(err, errBuildCacheMissExecutionNotImplemented) {
		t.Fatalf("executeCacheHits() = %v, want miss error", err)
	}
	assertEmptyDir(t, exec.scratchRoot)
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

func storeCacheLayer(t *testing.T, s store.Store, key, parent string, childData []byte) buildPlanStep {
	t.Helper()
	child := filepath.Join(t.TempDir(), "child.img")
	if err := os.WriteFile(child, childData, 0644); err != nil {
		t.Fatal(err)
	}
	delta, err := DiffDisks(parent, child)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := StoreDiskDelta(s, delta)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveBuildLayerManifest(s, manifest); err != nil {
		t.Fatal(err)
	}
	if err := saveBuildCacheEntry(s, buildCacheEntry{Key: key, LayerDigest: manifest.Digest}); err != nil {
		t.Fatal(err)
	}
	return buildPlanStep{Name: "cached", Key: key, CacheHit: true, LayerDigest: manifest.Digest}
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
