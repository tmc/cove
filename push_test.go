package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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
	if err := os.WriteFile(filepath.Join(vmPath, "machine.id"), []byte("machine"), 0644); err != nil {
		t.Fatalf("WriteFile(machine.id) error = %v", err)
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
	if got, want := len(plan.Prepared), len(plan.Chunks); got != want {
		t.Fatalf("prepared chunks = %d, want %d", got, want)
	}
	parsed, err := ociimage.ParseManifest(plan.Manifest)
	if err != nil {
		t.Fatalf("ParseManifest(): %v", err)
	}
	if !parsed.Chunks[0].Zero || !parsed.Chunks[2].Zero {
		t.Fatalf("zero chunks = %#v, want chunks 0 and 2 sparse", parsed.Chunks)
	}
	if parsed.DiskLayers[1].Descriptor.MediaType != ociimage.MediaTypeLayerLZ4 {
		t.Fatalf("non-zero layer media type = %q, want %q", parsed.DiskLayers[1].Descriptor.MediaType, ociimage.MediaTypeLayerLZ4)
	}
	if got, want := len(plan.Blobs), 3; got != want {
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
	if err := os.WriteFile(filepath.Join(vmPath, "machine.id"), []byte("machine"), 0644); err != nil {
		t.Fatalf("WriteFile(machine.id) error = %v", err)
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
	if err := os.WriteFile(filepath.Join(vmPath, "machine.id"), []byte("machine"), 0644); err != nil {
		t.Fatalf("WriteFile(machine.id) error = %v", err)
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

func TestPushImageUploadsRegistryContent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	vmPath := filepath.Join(GetVMBaseDir(), "dev-vm")
	if err := os.MkdirAll(vmPath, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	disk := []byte{0, 0, 0, 0, 1, 2, 3, 4}
	if err := os.WriteFile(filepath.Join(vmPath, "disk.img"), disk, 0644); err != nil {
		t.Fatalf("WriteFile(disk.img) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(vmPath, "aux.img"), []byte("aux"), 0644); err != nil {
		t.Fatalf("WriteFile(aux.img) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(vmPath, "hw.model"), []byte("hw"), 0644); err != nil {
		t.Fatalf("WriteFile(hw.model) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(vmPath, "machine.id"), []byte("machine"), 0644); err != nil {
		t.Fatalf("WriteFile(machine.id) error = %v", err)
	}

	uploaded := map[string][]byte{}
	manifests := map[string]ociimage.Manifest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const blobPrefix = "/v2/me/dev-vm/blobs/"
		const uploadPrefix = "/v2/me/dev-vm/blobs/uploads/"
		const manifestPrefix = "/v2/me/dev-vm/manifests/"
		switch {
		case r.Method == http.MethodHead && strings.HasPrefix(r.URL.Path, blobPrefix):
			digest := strings.TrimPrefix(r.URL.Path, blobPrefix)
			if _, ok := uploaded[digest]; ok {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPost && r.URL.Path == uploadPrefix:
			w.Header().Set("Location", uploadPrefix+"upload-id")
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodPut && r.URL.Path == uploadPrefix+"upload-id":
			digest := r.URL.Query().Get("digest")
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("ReadAll() error = %v", err)
			}
			uploaded[digest] = data
			w.WriteHeader(http.StatusCreated)
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, manifestPrefix):
			tag := strings.TrimPrefix(r.URL.Path, manifestPrefix)
			var manifest ociimage.Manifest
			if err := json.NewDecoder(r.Body).Decode(&manifest); err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			manifests[tag] = manifest
			w.WriteHeader(http.StatusCreated)
		default:
			t.Fatalf("%s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()

	opts := pushOptions{
		ChunkSize:       4,
		AdditionalTags:  stringList{"latest"},
		RegistryBaseURL: srv.URL,
	}
	plan, err := buildPushPlan("dev-vm", "ghcr.io/me/dev-vm:v1", opts)
	if err != nil {
		t.Fatalf("buildPushPlan(): %v", err)
	}
	if err := pushImage(context.Background(), plan, opts); err != nil {
		t.Fatalf("pushImage(): %v", err)
	}
	if _, ok := manifests["v1"]; !ok {
		t.Fatal("missing v1 manifest")
	}
	if _, ok := manifests["latest"]; !ok {
		t.Fatal("missing latest manifest")
	}
	if _, ok := uploaded[plan.Manifest.Config.Digest]; !ok {
		t.Fatalf("config digest %s was not uploaded", plan.Manifest.Config.Digest)
	}
	for _, blob := range plan.Blobs {
		if _, ok := uploaded[blob.Digest]; !ok {
			t.Fatalf("metadata digest %s was not uploaded", blob.Digest)
		}
	}
	for _, chunk := range plan.Prepared {
		if chunk.SkipUpload {
			if _, ok := uploaded[chunk.Chunk.Digest]; ok {
				t.Fatalf("zero chunk digest %s was uploaded", chunk.Chunk.Digest)
			}
			continue
		}
		if _, ok := uploaded[chunk.Descriptor.Digest]; !ok {
			t.Fatalf("chunk digest %s was not uploaded", chunk.Descriptor.Digest)
		}
	}
}

func TestHandlePushRequiresArgs(t *testing.T) {
	err := handlePush([]string{"dev-vm"})
	if err == nil || !strings.Contains(err.Error(), "usage: cove push") {
		t.Fatalf("handlePush() error = %v, want usage", err)
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
	if err := os.WriteFile(filepath.Join(vmPath, "machine.id"), []byte("machine"), 0644); err != nil {
		t.Fatalf("WriteFile(machine.id) error = %v", err)
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
