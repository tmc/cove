package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/vz-macos/internal/store"
)

func TestStoreAndApplyDiskDelta(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "parent.img")
	child := filepath.Join(dir, "child.img")
	restored := filepath.Join(dir, "restored.img")
	parentData := bytes.Repeat([]byte("a"), 4096)
	childData := append([]byte(nil), parentData...)
	copy(childData[2048:2052], []byte("bbbb"))
	if err := os.WriteFile(parent, parentData, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, childData, 0644); err != nil {
		t.Fatal(err)
	}
	delta, err := DiffDisks(parent, child)
	if err != nil {
		t.Fatalf("DiffDisks(): %v", err)
	}
	s := store.New(filepath.Join(dir, "store"))
	manifest, err := StoreDiskDelta(s, delta)
	if err != nil {
		t.Fatalf("StoreDiskDelta(): %v", err)
	}
	if manifest.Digest == "" {
		t.Fatal("manifest digest is empty")
	}
	if len(manifest.Blocks) != len(delta.Blocks) {
		t.Fatalf("manifest blocks = %d, want %d", len(manifest.Blocks), len(delta.Blocks))
	}
	if err := ApplyStoredDiskDelta(context.Background(), s, parent, restored, manifest); err != nil {
		t.Fatalf("ApplyStoredDiskDelta(): %v", err)
	}
	got, err := os.ReadFile(restored)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, childData) {
		t.Fatal("restored disk differs from child")
	}
}

func TestApplyStoredDiskDeltaRejectsMissingBlob(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "parent.img")
	child := filepath.Join(dir, "child.img")
	if err := os.WriteFile(parent, []byte("parent"), 0644); err != nil {
		t.Fatal(err)
	}
	s := store.New(filepath.Join(dir, "store"))
	manifest := buildLayerManifest{BlockSize: 4, DiskSize: 6, Blocks: []buildLayerBlock{{Offset: 0, Size: 1, Digest: digestBytes([]byte("x"))}}}
	if err := ApplyStoredDiskDelta(context.Background(), s, parent, child, manifest); err == nil {
		t.Fatal("ApplyStoredDiskDelta() error = nil, want missing blob")
	}
}
