package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
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
	if err := os.WriteFile(filepath.Join(vmPath, "hw.model"), []byte("hw"), 0644); err != nil {
		t.Fatalf("WriteFile(hw.model) error = %v", err)
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
	if got, want := len(plan.Blobs), 2; got != want {
		t.Fatalf("blobs = %d, want %d", got, want)
	}
	if plan.Manifest.Annotations[ociimage.CoveAuxDigest] != pushTestDigest([]byte("aux")) {
		t.Fatalf("manifest aux digest = %q", plan.Manifest.Annotations[ociimage.CoveAuxDigest])
	}
	if _, ok := plan.Manifest.Annotations[ociimage.LumeUncompressedDiskSize]; !ok {
		t.Fatalf("manifest missing lume compatibility annotations: %#v", plan.Manifest.Annotations)
	}
	if len(plan.ConfigJSON) == 0 {
		t.Fatal("ConfigJSON is empty")
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
	if err := os.WriteFile(filepath.Join(vmPath, "hw.model"), []byte("hw"), 0644); err != nil {
		t.Fatalf("WriteFile(hw.model) error = %v", err)
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

func TestHandlePushDryRunWritesManifest(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(vmPath, "hw.model"), []byte("hw"), 0644); err != nil {
		t.Fatalf("WriteFile(hw.model) error = %v", err)
	}
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")

	_, err := captureStdoutResult(t, func() error {
		return handlePush([]string{
			"--dry-run",
			"--chunk-size", "1",
			"--manifest-out", manifestPath,
			"dev-vm",
			"ghcr.io/me/dev-vm:v1",
		})
	})
	if err != nil {
		t.Fatalf("handlePush(): %v", err)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("ReadFile(manifest) error = %v", err)
	}
	var manifest ociimage.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("Unmarshal(manifest) error = %v", err)
	}
	parsed, err := ociimage.ParseManifest(manifest)
	if err != nil {
		t.Fatalf("ParseManifest(): %v", err)
	}
	if got, want := len(parsed.Chunks), 1; got != want {
		t.Fatalf("chunks = %d, want %d", got, want)
	}
}

func TestHandlePushRequiresDryRun(t *testing.T) {
	err := handlePush([]string{"dev-vm", "ghcr.io/me/dev-vm:v1"})
	if err == nil || !strings.Contains(err.Error(), "use --dry-run") {
		t.Fatalf("handlePush() error = %v, want dry-run guidance", err)
	}
}

func TestBuildPushPlanRejectsActiveVM(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "vzpush-")
	if err != nil {
		t.Fatalf("MkdirTemp() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home)
	vmPath := filepath.Join(home, ".vz", "vms", "dev-vm")
	if err := os.MkdirAll(vmPath, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(vmPath, "disk.img"), []byte{1, 2, 3}, 0644); err != nil {
		t.Fatalf("WriteFile(disk.img) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(vmPath, "aux.img"), []byte("aux"), 0644); err != nil {
		t.Fatalf("WriteFile(aux.img) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(vmPath, "hw.model"), []byte("hw"), 0644); err != nil {
		t.Fatalf("WriteFile(hw.model) error = %v", err)
	}
	ln := listenPushControlSocket(t, vmPath)
	defer ln.Close()

	_, err = buildPushPlan("dev-vm", "ghcr.io/me/dev-vm:v1", pushOptions{
		ChunkSize: 4,
		DryRun:    true,
	})
	if err == nil || !strings.Contains(err.Error(), `cannot push an active VM "dev-vm"`) {
		t.Fatalf("buildPushPlan() error = %v, want active VM", err)
	}
}

func TestBuildPushPlanRequiresMacMetadata(t *testing.T) {
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

	_, err := buildPushPlan("dev-vm", "ghcr.io/me/dev-vm:v1", pushOptions{
		ChunkSize: 4,
		DryRun:    true,
	})
	if err == nil || !strings.Contains(err.Error(), "macOS push requires hw.model") {
		t.Fatalf("buildPushPlan() error = %v, want hw.model requirement", err)
	}
}

func TestValidatePushReferences(t *testing.T) {
	tests := []struct {
		name    string
		ref     string
		opts    pushOptions
		wantErr string
	}{
		{
			name: "valid",
			ref:  "ghcr.io/me/dev-vm:v1",
			opts: pushOptions{
				BaseRef:        "ghcr.io/me/base@sha256:abcd",
				AdditionalTags: stringList{"latest"},
			},
		},
		{
			name:    "target missing tag",
			ref:     "ghcr.io/me/dev-vm",
			wantErr: "must include a tag",
		},
		{
			name:    "target digest",
			ref:     "ghcr.io/me/dev-vm:v1@sha256:abcd",
			wantErr: "must not include a digest",
		},
		{
			name:    "target missing registry",
			ref:     "me/dev-vm:v1",
			wantErr: "invalid target ref",
		},
		{
			name:    "base missing tag",
			ref:     "ghcr.io/me/dev-vm:v1",
			opts:    pushOptions{BaseRef: "ghcr.io/me/base"},
			wantErr: "must include a tag or digest",
		},
		{
			name:    "bad additional tag",
			ref:     "ghcr.io/me/dev-vm:v1",
			opts:    pushOptions{AdditionalTags: stringList{"bad/tag"}},
			wantErr: "invalid additional tag",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePushReferences(tt.ref, tt.opts)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("validatePushReferences() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validatePushReferences() error = %v, want containing %q", err, tt.wantErr)
			}
		})
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
		"--manifest-out", "manifest.json",
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
	if opts.ManifestOut != "manifest.json" {
		t.Fatalf("ManifestOut = %q, want manifest.json", opts.ManifestOut)
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

func listenPushControlSocket(t *testing.T, vmPath string) net.Listener {
	t.Helper()

	ln, err := net.Listen("unix", GetControlSocketPathForVM(vmPath))
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		if _, err := bufio.NewReader(conn).ReadBytes('\n'); err != nil {
			return
		}
		_, _ = conn.Write([]byte(`{"success":true}` + "\n"))
	}()
	return ln
}
