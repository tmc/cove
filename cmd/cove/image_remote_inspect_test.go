package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tmc/cove/internal/imagestore"
	"github.com/tmc/cove/internal/ociimage"
)

func TestInspectRemoteImageCoveManifest(t *testing.T) {
	manifest, _, _ := pullCompressedChunkedTestManifest(t, []byte("abcdefghijklmnop"), 4)
	srv := newOCIDispatchRegistry(t, manifest)
	t.Cleanup(srv.Close)

	out, err := InspectRemoteImage(context.Background(), "ghcr.io/me/dev-vm:v1", remoteInspectOptions{RegistryBaseURL: srv.URL})
	if err != nil {
		t.Fatalf("InspectRemoteImage: %v", err)
	}
	if out.Kind != "vm-oci" || out.Format != "cove" {
		t.Fatalf("kind/format = %s/%s, want vm-oci/cove", out.Kind, out.Format)
	}
	if out.ManifestDigest != "sha256:dispatch-test" {
		t.Fatalf("digest = %q, want dispatch-test", out.ManifestDigest)
	}
	if out.DiskSize != 16 || out.ChunkCount != 4 || out.DiskLayerCount != 4 {
		t.Fatalf("disk/chunks/layers = %d/%d/%d, want 16/4/4", out.DiskSize, out.ChunkCount, out.DiskLayerCount)
	}
	if out.CompressedDiskBytes == 0 {
		t.Fatal("compressed disk bytes = 0, want non-zero")
	}
}

func TestInspectRemoteImageResolvesIndex(t *testing.T) {
	manifest, _, _ := pullCompressedChunkedTestManifest(t, []byte("abcdefghijklmnop"), 4)
	manifestData, manifestDigest := pullTestManifestData(t, manifest)
	index := ociimage.Index{
		SchemaVersion: 2,
		MediaType:     ociimage.MediaTypeImageIndex,
		Manifests: []ociimage.IndexDescriptor{
			{
				Descriptor: ociimage.Descriptor{
					MediaType: ociimage.MediaTypeImageManifest,
					Size:      123,
					Digest:    "sha256:" + strings.Repeat("0", 64),
				},
				Platform: &ociimage.Platform{OS: "linux", Architecture: "amd64"},
			},
			{
				Descriptor: ociimage.Descriptor{
					MediaType: ociimage.MediaTypeImageManifest,
					Size:      int64(len(manifestData)),
					Digest:    manifestDigest,
				},
				Platform: &ociimage.Platform{OS: "darwin", Architecture: "arm64"},
			},
		},
	}
	srv := newRemoteInspectIndexRegistry(t, index, manifestData, manifestDigest)
	t.Cleanup(srv.Close)

	out, err := InspectRemoteImage(context.Background(), "ghcr.io/me/dev-vm:v1", remoteInspectOptions{RegistryBaseURL: srv.URL})
	if err != nil {
		t.Fatalf("InspectRemoteImage: %v", err)
	}
	if out.ManifestDigest != manifestDigest {
		t.Fatalf("manifest digest = %q, want %q", out.ManifestDigest, manifestDigest)
	}
	if out.Format != "cove" || out.DiskSize != 16 {
		t.Fatalf("format/disk = %s/%d, want cove/16", out.Format, out.DiskSize)
	}
}

func TestInspectRemoteImageLumeManifest(t *testing.T) {
	srv := newOCIDispatchRegistry(t, newLumeMockManifest())
	t.Cleanup(srv.Close)

	out, err := InspectRemoteImage(context.Background(), "ghcr.io/me/dev-vm:v1", remoteInspectOptions{RegistryBaseURL: srv.URL})
	if err != nil {
		t.Fatalf("InspectRemoteImage: %v", err)
	}
	if out.Kind != "vm-oci" || out.Format != "lume" {
		t.Fatalf("kind/format = %s/%s, want vm-oci/lume", out.Kind, out.Format)
	}
	if out.DiskPartCount != 2 || out.CompressedDiskBytes != 192 {
		t.Fatalf("parts/compressed = %d/%d, want 2/192", out.DiskPartCount, out.CompressedDiskBytes)
	}
	if out.ConfigBytes != 32 || out.NVRAMBytes != 1024 {
		t.Fatalf("config/nvram = %d/%d, want 32/1024", out.ConfigBytes, out.NVRAMBytes)
	}
}

func TestInspectRemoteImageTartManifest(t *testing.T) {
	manifest, _ := newTartMockManifest(t, [][]byte{[]byte("first"), []byte("second")})
	srv := newOCIDispatchRegistry(t, manifest)
	t.Cleanup(srv.Close)

	out, err := InspectRemoteImage(context.Background(), "ghcr.io/me/dev-vm:v1", remoteInspectOptions{RegistryBaseURL: srv.URL})
	if err != nil {
		t.Fatalf("InspectRemoteImage: %v", err)
	}
	if out.Kind != "vm-oci" || out.Format != "tart" {
		t.Fatalf("kind/format = %s/%s, want vm-oci/tart", out.Kind, out.Format)
	}
	if out.DiskSize != 11 || out.ChunkCount != 2 || out.DiskLayerCount != 2 {
		t.Fatalf("disk/chunks/layers = %d/%d/%d, want 11/2/2", out.DiskSize, out.ChunkCount, out.DiskLayerCount)
	}
	if out.UploadTime != "2026-04-26T00:00:00Z" {
		t.Fatalf("upload time = %q", out.UploadTime)
	}
	if out.ConfigBytes == 0 || out.NVRAMBytes == 0 || out.CompressedDiskBytes == 0 {
		t.Fatalf("config/nvram/compressed = %d/%d/%d, want non-zero", out.ConfigBytes, out.NVRAMBytes, out.CompressedDiskBytes)
	}
}

func TestInspectRemoteImageCoveImageArtifact(t *testing.T) {
	created := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	config := imagestore.Manifest{
		SchemaVersion: 1,
		Name:          "runner",
		Tag:           "v1",
		DiskSHA256:    strings.Repeat("a", 64),
		DiskSize:      99,
		CreatedAt:     created,
		BuiltAt:       created,
	}
	configBytes, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	configDigest := digestData(configBytes)
	manifest := ociimage.Manifest{
		SchemaVersion: 2,
		MediaType:     ociimage.MediaTypeImageManifest,
		Config: ociimage.Descriptor{
			MediaType: coveImageConfigType,
			Size:      int64(len(configBytes)),
			Digest:    configDigest,
		},
		Layers: []ociimage.Descriptor{
			{
				MediaType:   coveImageDiskType,
				Size:        42,
				Digest:      "sha256:" + strings.Repeat("b", 64),
				Annotations: map[string]string{ociTitleAnnotation: "disk.img.gz"},
			},
			{
				MediaType:   coveImageFileType,
				Size:        7,
				Digest:      "sha256:" + strings.Repeat("c", 64),
				Annotations: map[string]string{ociTitleAnnotation: "aux.img"},
			},
		},
	}
	srv := newRemoteInspectRegistry(t, manifest, map[string][]byte{configDigest: configBytes})
	t.Cleanup(srv.Close)

	out, err := InspectRemoteImage(context.Background(), "ghcr.io/me/dev-vm:v1", remoteInspectOptions{RegistryBaseURL: srv.URL})
	if err != nil {
		t.Fatalf("InspectRemoteImage: %v", err)
	}
	if out.Kind != "image-store" || out.Format != "cove-image" {
		t.Fatalf("kind/format = %s/%s, want image-store/cove-image", out.Kind, out.Format)
	}
	if out.ImageRef != "runner:v1" || out.DiskSize != 99 || out.DiskSHA256 != config.DiskSHA256 {
		t.Fatalf("image/disk = %q/%d/%q", out.ImageRef, out.DiskSize, out.DiskSHA256)
	}
	if out.CompressedDiskBytes != 42 || out.MetadataBlobs != 1 || out.MetadataBytes != 7 {
		t.Fatalf("compressed/meta = %d/%d/%d, want 42/1/7", out.CompressedDiskBytes, out.MetadataBlobs, out.MetadataBytes)
	}
}

func TestWriteRemoteInspectText(t *testing.T) {
	var b strings.Builder
	err := writeRemoteInspectText(&b, ImageRemoteInspectOutput{
		Ref:                 "ghcr.io/me/dev-vm:v1",
		ManifestDigest:      "sha256:test",
		Kind:                "vm-oci",
		Format:              "cove",
		DiskSize:            16,
		CompressedDiskBytes: 8,
		ChunkCount:          2,
		LayerCount:          3,
		TotalLayerBytes:     9,
	})
	if err != nil {
		t.Fatalf("writeRemoteInspectText: %v", err)
	}
	for _, want := range []string{"Remote image ghcr.io/me/dev-vm:v1", "format:          cove", "chunks:          2"} {
		if !strings.Contains(b.String(), want) {
			t.Fatalf("text missing %q:\n%s", want, b.String())
		}
	}
}

func TestMoveImageInspectFlagsFirst(t *testing.T) {
	got := strings.Join(moveImageInspectFlagsFirst([]string{
		"registry.example.com/team/vm:v1",
		"-remote",
		"-json",
	}), " ")
	want := "-remote -json registry.example.com/team/vm:v1"
	if got != want {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

func newRemoteInspectRegistry(t *testing.T, manifest ociimage.Manifest, blobs map[string][]byte) *httptest.Server {
	t.Helper()
	manifestData, manifestDigest := pullTestManifestData(t, manifest)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/me/dev-vm/manifests/v1":
			w.Header().Set("Content-Type", ociimage.MediaTypeImageManifest)
			w.Header().Set("Docker-Content-Digest", manifestDigest)
			_, _ = w.Write(manifestData)
		case strings.HasPrefix(r.URL.Path, "/v2/me/dev-vm/blobs/"):
			digest := strings.TrimPrefix(r.URL.Path, "/v2/me/dev-vm/blobs/")
			data, ok := blobs[digest]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(data)
		default:
			http.NotFound(w, r)
		}
	}))
}

func newRemoteInspectIndexRegistry(t *testing.T, index ociimage.Index, manifestData []byte, manifestDigest string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/me/dev-vm/manifests/v1":
			w.Header().Set("Content-Type", ociimage.MediaTypeImageIndex)
			w.Header().Set("Docker-Content-Digest", "sha256:index")
			if err := json.NewEncoder(w).Encode(index); err != nil {
				t.Errorf("encode index: %v", err)
			}
		case "/v2/me/dev-vm/manifests/" + manifestDigest:
			w.Header().Set("Content-Type", ociimage.MediaTypeImageManifest)
			w.Header().Set("Docker-Content-Digest", manifestDigest)
			_, _ = w.Write(manifestData)
		default:
			http.NotFound(w, r)
		}
	}))
}
