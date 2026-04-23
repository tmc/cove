package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/vz-macos/internal/ociimage"
)

func TestBuildPushPlan(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	vmPath := filepath.Join(GetVMBaseDir(), "dev-vm")
	if err := os.MkdirAll(vmPath, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	disk := []byte{0, 0, 0, 0, 1, 2, 3, 4, 0, 0}
	if err := os.WriteFile(filepath.Join(vmPath, "disk.img"), disk, 0644); err != nil {
		t.Fatalf("WriteFile(disk.img) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(vmPath, "aux.img"), []byte("aux"), 0644); err != nil {
		t.Fatalf("WriteFile(aux.img) error = %v", err)
	}

	plan, err := buildPushPlan("dev-vm", "ghcr.io/me/dev-vm:v1", pushOptions{
		BaseRef:        "ghcr.io/me/base:v1",
		ChunkSize:      4,
		DryRun:         true,
		LumeCompat:     true,
		AdditionalTags: stringList{"latest"},
	})
	if err != nil {
		t.Fatalf("buildPushPlan(): %v", err)
	}
	if plan.DiskSize != int64(len(disk)) {
		t.Fatalf("DiskSize = %d, want %d", plan.DiskSize, len(disk))
	}
	if got, want := len(plan.Chunks), 3; got != want {
		t.Fatalf("chunks = %d, want %d", got, want)
	}
	if plan.ZeroChunks != 2 || plan.ZeroBytes != 6 {
		t.Fatalf("zero summary = (%d, %d), want (2, 6)", plan.ZeroChunks, plan.ZeroBytes)
	}
	if plan.Chunks[1].Digest != pushTestDigest(disk[4:8]) {
		t.Fatalf("chunk digest = %q, want %q", plan.Chunks[1].Digest, pushTestDigest(disk[4:8]))
	}
}

func TestHandlePushDryRunOutput(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	vmPath := filepath.Join(GetVMBaseDir(), "dev-vm")
	if err := os.MkdirAll(vmPath, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(vmPath, "disk.img"), []byte{1, 2, 3}, 0644); err != nil {
		t.Fatalf("WriteFile(disk.img) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(vmPath, "aux.img"), []byte("aux"), 0644); err != nil {
		t.Fatalf("WriteFile(aux.img) error = %v", err)
	}

	out, err := captureStdoutResult(t, func() error {
		return handlePush([]string{"--dry-run", "--chunk-size", "1", "dev-vm", "ghcr.io/me/dev-vm:v1"})
	})
	if err != nil {
		t.Fatalf("handlePush(): %v", err)
	}
	for _, want := range []string{
		"Push dry run",
		"vm: dev-vm",
		"ref: ghcr.io/me/dev-vm:v1",
		"chunks: 1",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output %q missing %q", out, want)
		}
	}
}

func TestHandlePushRequiresDryRun(t *testing.T) {
	err := handlePush([]string{"dev-vm", "ghcr.io/me/dev-vm:v1"})
	if err == nil || !strings.Contains(err.Error(), "use --dry-run") {
		t.Fatalf("handlePush() error = %v, want dry-run guidance", err)
	}
}

func TestParsePushArgs(t *testing.T) {
	opts, pos, err := parsePushArgs([]string{
		"--base", "base",
		"--chunk-size", "256",
		"--dry-run",
		"--lume-compat",
		"--additional-tag", "latest",
		"--additional-tag", "stable",
		"vm",
		"ref",
	}, ioDiscard{})
	if err != nil {
		t.Fatalf("parsePushArgs(): %v", err)
	}
	if opts.ChunkSize != 256<<20 {
		t.Fatalf("ChunkSize = %d, want %d", opts.ChunkSize, int64(256<<20))
	}
	if !opts.DryRun || !opts.LumeCompat || opts.BaseRef != "base" {
		t.Fatalf("opts = %#v", opts)
	}
	if got := strings.Join(opts.AdditionalTags, ","); got != "latest,stable" {
		t.Fatalf("AdditionalTags = %q, want latest,stable", got)
	}
	if strings.Join(pos, ",") != "vm,ref" {
		t.Fatalf("pos = %#v", pos)
	}
}

func TestParsePushArgsRejectsBadChunkSize(t *testing.T) {
	_, _, err := parsePushArgs([]string{"--chunk-size", "0", "vm", "ref"}, ioDiscard{})
	if err == nil || !strings.Contains(err.Error(), "invalid chunk size") {
		t.Fatalf("parsePushArgs() error = %v, want invalid chunk size", err)
	}
}

func TestPushLayerAnnotations(t *testing.T) {
	chunk := ociimage.Chunk{Index: 0, Size: 3, Digest: pushTestDigest([]byte{1, 2, 3})}
	annotations := ociimage.ChunkLayerAnnotations(chunk, 1)
	if annotations[ociimage.CoveRole] != "disk" {
		t.Fatalf("CoveRole = %q, want disk", annotations[ociimage.CoveRole])
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) {
	return len(p), nil
}

func pushTestDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
