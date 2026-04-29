package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestDiffDisksAndApply(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "parent.img")
	child := filepath.Join(dir, "child.img")
	restored := filepath.Join(dir, "restored.img")
	parentData := bytes.Repeat([]byte{0}, buildDeltaBlockSize*2+17)
	copy(parentData[100:105], []byte("hello"))
	childData := append([]byte(nil), parentData...)
	copy(childData[buildDeltaBlockSize+9:buildDeltaBlockSize+14], []byte("there"))
	childData = append(childData, []byte("tail")...)
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
	if len(delta.Blocks) != 2 {
		t.Fatalf("changed blocks = %d, want 2", len(delta.Blocks))
	}
	if err := ApplyDiskDelta(parent, restored, delta); err != nil {
		t.Fatalf("ApplyDiskDelta(): %v", err)
	}
	got, err := os.ReadFile(restored)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, childData) {
		t.Fatal("restored disk differs from child")
	}
}

func TestDiffDisksRecordsZeroedBlocks(t *testing.T) {
	parent := bytes.NewReader([]byte{1, 2, 3, 4})
	child := bytes.NewReader([]byte{1, 0, 0, 4})
	delta, err := diffDiskReaders(parent, child, 4, 4)
	if err != nil {
		t.Fatalf("diffDiskReaders(): %v", err)
	}
	if len(delta.Blocks) != 1 {
		t.Fatalf("changed blocks = %d, want 1", len(delta.Blocks))
	}
	if !bytes.Equal(delta.Blocks[0].Data, []byte{1, 0, 0, 4}) {
		t.Fatalf("block data = %v, want [1 0 0 4]", delta.Blocks[0].Data)
	}
}

func TestApplyDiskDeltaRejectsInvalidBlocks(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "parent.img")
	child := filepath.Join(dir, "child.img")
	if err := os.WriteFile(parent, []byte("parent"), 0644); err != nil {
		t.Fatal(err)
	}
	err := ApplyDiskDelta(parent, child, &diskDelta{BlockSize: 4, Size: 2, Blocks: []diskDeltaBlock{{Offset: 4, Data: []byte{1}}}})
	if err == nil {
		t.Fatal("ApplyDiskDelta() error = nil, want invalid block")
	}
}
