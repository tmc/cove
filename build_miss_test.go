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

func TestRecordCacheMissLayerStoresMetadata(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent.img")
	child := filepath.Join(root, "child.img")
	if err := os.WriteFile(parent, []byte("base image\n"), 0644); err != nil {
		t.Fatal(err)
	}
	want := []byte("built image\n")
	if err := os.WriteFile(child, want, 0644); err != nil {
		t.Fatal(err)
	}
	exec := testBuildExecutor(filepath.Join(root, "scratch"))
	exec.store = store.New(filepath.Join(root, "store"))
	step := buildPlanStep{
		Name:                 "install",
		Key:                  "sha256:" + strings.Repeat("1", 64),
		ParentDigest:         "sha256:" + strings.Repeat("2", 64),
		ScriptDigest:         "sha256:" + strings.Repeat("3", 64),
		AgentProtocolVersion: agentProtocolVersion,
		Meta:                 buildScriptMeta{Compact: "targeted"},
	}
	result, err := exec.recordCacheMissLayer(context.Background(), step, parent, child)
	if err != nil {
		t.Fatalf("recordCacheMissLayer(): %v", err)
	}
	if result.Step != step.Name || result.Key != step.Key || result.LayerDigest == "" || result.DiskPath != child {
		t.Fatalf("result = %#v", result)
	}
	entry, err := loadBuildCacheEntry(exec.store, step.Key)
	if err != nil {
		t.Fatal(err)
	}
	if entry.ParentDigest != step.ParentDigest || entry.ScriptDigest != step.ScriptDigest || entry.Compact != "targeted" {
		t.Fatalf("entry = %#v", entry)
	}
	manifest, err := loadBuildLayerManifest(exec.store, entry.LayerDigest)
	if err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(root, "restored.img")
	if err := ApplyStoredDiskDelta(context.Background(), exec.store, parent, restored, manifest); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(restored)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("restored disk = %q, want %q", got, want)
	}
}

func TestRecordCacheMissLayerValidatesBeforeStore(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent.img")
	child := filepath.Join(root, "child.img")
	if err := os.WriteFile(parent, []byte("base"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, []byte("child"), 0644); err != nil {
		t.Fatal(err)
	}
	exec := testBuildExecutor(filepath.Join(root, "scratch"))
	exec.store = store.New(filepath.Join(root, "store"))
	_, err := exec.recordCacheMissLayer(context.Background(), buildPlanStep{Name: "bad", Key: "not-a-digest"}, parent, child)
	if err == nil {
		t.Fatal("recordCacheMissLayer() error = nil, want invalid key")
	}
	assertEmptyDir(t, filepath.Join(exec.store.Dir, "build-cache"))
}

func TestRecordCacheMissLayerValidatesMetadataBeforeStore(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent.img")
	child := filepath.Join(root, "child.img")
	if err := os.WriteFile(parent, []byte("base"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, []byte("child"), 0644); err != nil {
		t.Fatal(err)
	}
	exec := testBuildExecutor(filepath.Join(root, "scratch"))
	exec.store = store.New(filepath.Join(root, "store"))
	step := testBuildPlanStep("missing-metadata", "sha256:"+strings.Repeat("1", 64))
	step.ParentDigest = ""
	_, err := exec.recordCacheMissLayer(context.Background(), step, parent, child)
	if err == nil {
		t.Fatal("recordCacheMissLayer() error = nil, want missing metadata")
	}
	assertEmptyDir(t, filepath.Join(exec.store.Dir, "build-cache"))
}

func TestRecordCacheMissLayerHonorsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	exec := testBuildExecutor(t.TempDir())
	_, err := exec.recordCacheMissLayer(ctx, buildPlanStep{Key: "sha256:" + strings.Repeat("1", 64)}, "parent.img", "child.img")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("recordCacheMissLayer() = %v, want context.Canceled", err)
	}
}
