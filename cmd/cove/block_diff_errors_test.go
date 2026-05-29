package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiffDiskReadersValidation(t *testing.T) {
	r := bytes.NewReader(nil)
	tests := []struct {
		name      string
		blockSize int64
		childSize int64
		want      string
	}{
		{name: "zero block size", blockSize: 0, childSize: 4, want: "invalid block size"},
		{name: "negative block size", blockSize: -1, childSize: 4, want: "invalid block size"},
		{name: "negative child size", blockSize: 4, childSize: -1, want: "invalid child size"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := diffDiskReaders(r, r, tt.childSize, tt.blockSize)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("diffDiskReaders() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestDiffDisksMissingFiles(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.img")
	if err := os.WriteFile(good, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "nope.img")

	if _, err := DiffDisks(missing, good); err == nil || !strings.Contains(err.Error(), "diff parent") {
		t.Fatalf("DiffDisks(missing parent) error = %v, want diff parent", err)
	}
	if _, err := DiffDisks(good, missing); err == nil || !strings.Contains(err.Error(), "diff child") {
		t.Fatalf("DiffDisks(missing child) error = %v, want diff child", err)
	}
}

func TestApplyDiskDeltaValidation(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Join(dir, "parent.img")
	if err := os.WriteFile(parent, []byte("parent"), 0644); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(dir, "child.img")

	tests := []struct {
		name  string
		delta *diskDelta
		want  string
	}{
		{name: "nil delta", delta: nil, want: "nil delta"},
		{name: "zero block size", delta: &diskDelta{BlockSize: 0, Size: 4}, want: "invalid block size"},
		{name: "negative size", delta: &diskDelta{BlockSize: 4, Size: -1}, want: "invalid size"},
		{
			name:  "negative offset",
			delta: &diskDelta{BlockSize: 4, Size: 8, Blocks: []diskDeltaBlock{{Offset: -4, Data: []byte{0}}}},
			want:  "invalid offset",
		},
		{
			name:  "misaligned offset",
			delta: &diskDelta{BlockSize: 4, Size: 8, Blocks: []diskDeltaBlock{{Offset: 3, Data: []byte{0}}}},
			want:  "invalid offset",
		},
		{
			name:  "data larger than block",
			delta: &diskDelta{BlockSize: 4, Size: 8, Blocks: []diskDeltaBlock{{Offset: 0, Data: []byte{1, 2, 3, 4, 5}}}},
			want:  "invalid block",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ApplyDiskDelta(parent, child, tt.delta)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ApplyDiskDelta() error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestApplyDiskDeltaMissingParent(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "nope.img")
	child := filepath.Join(dir, "child.img")
	err := ApplyDiskDelta(missing, child, &diskDelta{BlockSize: 4, Size: 4})
	if err == nil || !strings.Contains(err.Error(), "copy parent") {
		t.Fatalf("ApplyDiskDelta() error = %v, want copy parent", err)
	}
}
