package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/store"
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
	step := testBuildPlanStep("missing", key)
	if err := saveBuildCacheEntry(exec.store, testCacheEntryForStep(step, "sha256:"+strings.Repeat("b", 64))); err != nil {
		t.Fatal(err)
	}
	_, err := exec.applyCacheHit(context.Background(), step, "parent.img")
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
	manifest := buildLayerManifest{
		BlockSize: buildDeltaBlockSize,
		DiskSize:  4,
		Blocks: []buildLayerBlock{{
			Offset: 0,
			Size:   4,
			Digest: "sha256:" + strings.Repeat("c", 64),
		}},
	}
	layer, err := digestBuildLayerManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Digest = layer
	if err := saveBuildLayerManifest(exec.store, manifest); err != nil {
		t.Fatal(err)
	}
	step := testBuildPlanStep("missing-block", key)
	if err := saveBuildCacheEntry(exec.store, testCacheEntryForStep(step, layer)); err != nil {
		t.Fatal(err)
	}
	_, err = exec.applyCacheHit(context.Background(), step, "parent.img")
	if err == nil {
		t.Fatal("applyCacheHit() error = nil, want missing block")
	}
	assertEmptyDir(t, exec.scratchRoot)
}

func TestApplyCacheHitValidatesEntryMetadataBeforeScratch(t *testing.T) {
	root := t.TempDir()
	exec := testBuildExecutor(filepath.Join(root, "scratch"))
	exec.store = store.New(filepath.Join(root, "store"))
	key := "sha256:" + strings.Repeat("a", 64)
	layer := "sha256:" + strings.Repeat("b", 64)
	step := testBuildPlanStep("mismatch", key)
	entry := testCacheEntryForStep(step, layer)
	entry.ScriptDigest = digestBytes([]byte("other script"))
	if err := saveBuildCacheEntry(exec.store, entry); err != nil {
		t.Fatal(err)
	}
	_, err := exec.applyCacheHit(context.Background(), step, "parent.img")
	if err == nil {
		t.Fatal("applyCacheHit() error = nil, want metadata mismatch")
	}
	if !strings.Contains(err.Error(), "script digest") {
		t.Fatalf("applyCacheHit() error = %v, want script digest mismatch", err)
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

func TestApplyCacheHitVMMaterializesBundle(t *testing.T) {
	root := t.TempDir()
	parentDir := filepath.Join(root, "parent")
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"disk.img":      "base image\n",
		"aux.img":       "aux",
		"hw.model":      "hw",
		"machine.id":    "machine",
		"config.json":   "{}",
		"control.token": "token",
	} {
		if err := os.WriteFile(filepath.Join(parentDir, name), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	s := store.New(filepath.Join(root, "store"))
	step := storeCacheLayer(t, s, "sha256:"+strings.Repeat("1", 64), filepath.Join(parentDir, "disk.img"), []byte("cached image\n"))
	exec := testBuildExecutor(filepath.Join(root, "scratch"))
	exec.store = s
	result, err := exec.applyCacheHitVM(context.Background(), step, parentDir)
	if err != nil {
		t.Skipf("clonefile unsupported for cache-hit vm test: %v", err)
	}
	if got := readFile(t, result.DiskPath); got != "cached image\n" {
		t.Fatalf("scratch disk = %q, want cached image", got)
	}
	for name, want := range map[string]string{
		"aux.img":       "aux",
		"hw.model":      "hw",
		"machine.id":    "machine",
		"config.json":   "{}",
		"control.token": "token",
	} {
		if got := readFile(t, filepath.Join(result.Scratch.Dir, name)); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestApplyCacheHitVMValidatesBeforeScratch(t *testing.T) {
	root := t.TempDir()
	parentDir := filepath.Join(root, "parent")
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parentDir, "disk.img"), []byte("base image\n"), 0644); err != nil {
		t.Fatal(err)
	}
	exec := testBuildExecutor(filepath.Join(root, "scratch"))
	_, err := exec.applyCacheHitVM(context.Background(), buildPlanStep{Name: "bad", Key: "not-a-digest"}, parentDir)
	if err == nil {
		t.Fatal("applyCacheHitVM() error = nil, want invalid key")
	}
	assertEmptyDir(t, exec.scratchRoot)
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

func TestExecuteWithMissRunnerRecordsAndChains(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent.img")
	if err := os.WriteFile(parent, []byte("base image\n"), 0644); err != nil {
		t.Fatal(err)
	}
	s := store.New(filepath.Join(root, "store"))
	hit := storeCacheLayer(t, s, "sha256:"+strings.Repeat("1", 64), parent, []byte("cached image\n"))
	miss := buildPlanStep{
		Name:                 "miss",
		Key:                  "sha256:" + strings.Repeat("2", 64),
		ParentDigest:         hit.Key,
		ScriptDigest:         "sha256:" + strings.Repeat("3", 64),
		AgentProtocolVersion: agentProtocolVersion,
		Meta:                 buildScriptMeta{Compact: "targeted"},
	}
	exec := testBuildExecutor(filepath.Join(root, "scratch"))
	exec.store = s
	exec.opts.KeepIntermediate = false
	exec.plan.Steps = []buildPlanStep{hit, miss}
	result, err := exec.executeWithMissRunner(context.Background(), parent, func(ctx context.Context, step buildPlanStep, sc buildScratch) error {
		got, err := os.ReadFile(sc.DiskPath)
		if err != nil {
			return err
		}
		if string(got) != "cached image\n" {
			return fmt.Errorf("scratch parent = %q, want cached image", got)
		}
		return os.WriteFile(sc.DiskPath, []byte("built image\n"), 0644)
	})
	if err != nil {
		t.Fatalf("executeWithMissRunner(): %v", err)
	}
	if len(result.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(result.Steps))
	}
	got, err := os.ReadFile(result.DiskPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "built image\n" {
		t.Fatalf("final disk = %q, want built image", got)
	}
	entry, err := loadBuildCacheEntry(s, miss.Key)
	if err != nil {
		t.Fatal(err)
	}
	if entry.ParentDigest != miss.ParentDigest || entry.ScriptDigest != miss.ScriptDigest {
		t.Fatalf("entry = %#v", entry)
	}
	entries, err := os.ReadDir(exec.scratchRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("scratch entries = %d, want final scratch only", len(entries))
	}
}

func TestExecuteWithMissRunnerFailureCleansScratch(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent.img")
	if err := os.WriteFile(parent, []byte("base image\n"), 0644); err != nil {
		t.Fatal(err)
	}
	miss := buildPlanStep{Name: "miss", Key: "sha256:" + strings.Repeat("2", 64)}
	exec := testBuildExecutor(filepath.Join(root, "scratch"))
	exec.opts.KeepIntermediate = false
	exec.plan.Steps = []buildPlanStep{miss}
	wantErr := errors.New("guest failed")
	_, err := exec.executeWithMissRunner(context.Background(), parent, func(context.Context, buildPlanStep, buildScratch) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("executeWithMissRunner() = %v, want guest failure", err)
	}
	assertEmptyDir(t, exec.scratchRoot)
	if _, err := loadBuildCacheEntry(exec.store, miss.Key); err == nil {
		t.Fatal("cache entry exists after miss failure")
	}
}

func TestExecuteWithMissRunnerFailureKeepsScratch(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent.img")
	if err := os.WriteFile(parent, []byte("base image\n"), 0644); err != nil {
		t.Fatal(err)
	}
	miss := buildPlanStep{Name: "miss", Key: "sha256:" + strings.Repeat("2", 64)}
	exec := testBuildExecutor(filepath.Join(root, "scratch"))
	exec.opts.KeepIntermediate = true
	exec.plan.Steps = []buildPlanStep{miss}
	wantErr := errors.New("guest failed")
	_, err := exec.executeWithMissRunner(context.Background(), parent, func(context.Context, buildPlanStep, buildScratch) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("executeWithMissRunner() = %v, want guest failure", err)
	}
	if !strings.Contains(err.Error(), "scratch kept at") {
		t.Fatalf("executeWithMissRunner() = %v, want scratch path", err)
	}
	entries, readErr := os.ReadDir(exec.scratchRoot)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 1 {
		t.Fatalf("scratch entries = %d, want 1", len(entries))
	}
	if _, err := loadBuildCacheEntry(exec.store, miss.Key); err == nil {
		t.Fatal("cache entry exists after miss failure")
	}
}

func TestExecuteVMWithMissRunnerRecordsAndChains(t *testing.T) {
	root := t.TempDir()
	parentDir := filepath.Join(root, "parent")
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"disk.img":      "base image\n",
		"aux.img":       "aux",
		"hw.model":      "hw",
		"machine.id":    "machine",
		"config.json":   "{}",
		"control.token": "token",
	} {
		if err := os.WriteFile(filepath.Join(parentDir, name), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	s := store.New(filepath.Join(root, "store"))
	hit := storeCacheLayer(t, s, "sha256:"+strings.Repeat("1", 64), filepath.Join(parentDir, "disk.img"), []byte("cached image\n"))
	miss := buildPlanStep{
		Name:                 "miss",
		Key:                  "sha256:" + strings.Repeat("2", 64),
		ParentDigest:         hit.Key,
		ScriptDigest:         "sha256:" + strings.Repeat("3", 64),
		AgentProtocolVersion: agentProtocolVersion,
		Meta:                 buildScriptMeta{Compact: "targeted"},
	}
	exec := testBuildExecutor(filepath.Join(root, "scratch"))
	exec.store = s
	exec.opts.KeepIntermediate = false
	exec.plan.Steps = []buildPlanStep{hit, miss}
	result, err := exec.executeVMWithMissRunner(context.Background(), parentDir, func(ctx context.Context, step buildPlanStep, sc buildScratch) error {
		got, err := os.ReadFile(sc.DiskPath)
		if err != nil {
			return err
		}
		if string(got) != "cached image\n" {
			return fmt.Errorf("scratch parent = %q, want cached image", got)
		}
		if got := readFile(t, filepath.Join(sc.Dir, "aux.img")); got != "aux" {
			return fmt.Errorf("scratch aux = %q, want aux", got)
		}
		return os.WriteFile(sc.DiskPath, []byte("built image\n"), 0644)
	})
	if err != nil {
		t.Skipf("clonefile unsupported for vm execution test: %v", err)
	}
	if len(result.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(result.Steps))
	}
	if result.VMDir == "" || result.DiskPath == "" {
		t.Fatalf("result = %#v, want vm dir and disk path", result)
	}
	if got := readFile(t, result.DiskPath); got != "built image\n" {
		t.Fatalf("final disk = %q, want built image", got)
	}
	entry, err := loadBuildCacheEntry(s, miss.Key)
	if err != nil {
		t.Fatal(err)
	}
	if entry.ParentDigest != miss.ParentDigest || entry.ScriptDigest != miss.ScriptDigest {
		t.Fatalf("entry = %#v", entry)
	}
	entries, err := os.ReadDir(exec.scratchRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("scratch entries = %d, want final scratch only", len(entries))
	}
}

func TestExecuteVMWithMissRunnerStopsAtMissAndCleansScratch(t *testing.T) {
	root := t.TempDir()
	parentDir := filepath.Join(root, "parent")
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"disk.img":   "base image\n",
		"aux.img":    "aux",
		"hw.model":   "hw",
		"machine.id": "machine",
	} {
		if err := os.WriteFile(filepath.Join(parentDir, name), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	s := store.New(filepath.Join(root, "store"))
	hit := storeCacheLayer(t, s, "sha256:"+strings.Repeat("1", 64), filepath.Join(parentDir, "disk.img"), []byte("cached image\n"))
	miss := buildPlanStep{Name: "miss", Key: "sha256:" + strings.Repeat("2", 64)}
	exec := testBuildExecutor(filepath.Join(root, "scratch"))
	exec.store = s
	exec.opts.KeepIntermediate = false
	exec.plan.Steps = []buildPlanStep{hit, miss}
	_, err := exec.executeVMWithMissRunner(context.Background(), parentDir, nil)
	if err != nil && strings.Contains(err.Error(), "clonefile") {
		t.Skipf("clonefile unsupported for vm execution test: %v", err)
	}
	if !errors.Is(err, errBuildCacheMissExecutionNotImplemented) {
		t.Fatalf("executeVMWithMissRunner() = %v, want miss error", err)
	}
	assertEmptyDir(t, exec.scratchRoot)
}

func TestExecuteVMWithMissRunnerFailureKeepsScratch(t *testing.T) {
	root := t.TempDir()
	parentDir := filepath.Join(root, "parent")
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"disk.img":   "base image\n",
		"aux.img":    "aux",
		"hw.model":   "hw",
		"machine.id": "machine",
	} {
		if err := os.WriteFile(filepath.Join(parentDir, name), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	miss := buildPlanStep{Name: "miss", Key: "sha256:" + strings.Repeat("2", 64)}
	exec := testBuildExecutor(filepath.Join(root, "scratch"))
	exec.opts.KeepIntermediate = true
	exec.plan.Steps = []buildPlanStep{miss}
	wantErr := errors.New("guest failed")
	_, err := exec.executeVMWithMissRunner(context.Background(), parentDir, func(context.Context, buildPlanStep, buildScratch) error {
		return wantErr
	})
	if err != nil && strings.Contains(err.Error(), "clonefile") {
		t.Skipf("clonefile unsupported for vm execution test: %v", err)
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("executeVMWithMissRunner() = %v, want guest failure", err)
	}
	if !strings.Contains(err.Error(), "scratch kept at") {
		t.Fatalf("executeVMWithMissRunner() = %v, want scratch path", err)
	}
	entries, readErr := os.ReadDir(exec.scratchRoot)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 1 {
		t.Fatalf("scratch entries = %d, want 1", len(entries))
	}
	if _, err := loadBuildCacheEntry(exec.store, miss.Key); err == nil {
		t.Fatal("cache entry exists after miss failure")
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
	step = testBuildPlanStep("cached", key)
	step.CacheHit = true
	step.LayerDigest = manifest.Digest
	if err := saveBuildCacheEntry(exec.store, testCacheEntryForStep(step, manifest.Digest)); err != nil {
		t.Fatal(err)
	}
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
	step := testBuildPlanStep("cached", key)
	step.CacheHit = true
	step.LayerDigest = manifest.Digest
	if err := saveBuildCacheEntry(s, testCacheEntryForStep(step, manifest.Digest)); err != nil {
		t.Fatal(err)
	}
	return step
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
