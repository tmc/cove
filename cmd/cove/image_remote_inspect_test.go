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
	manifest.Annotations = cloneStringMap(manifest.Annotations)
	manifest.Annotations[ociimage.CoveDiskFormat] = "asif"
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
	if out.DiskFormat != "asif" {
		t.Fatalf("disk format = %q, want asif", out.DiskFormat)
	}
	if out.PullPlan != "cove chunked pull" || !strings.Contains(out.Verification, "chunk digests verified") {
		t.Fatalf("pull/verification = %q/%q", out.PullPlan, out.Verification)
	}
	if out.CompressedDiskBytes == 0 {
		t.Fatal("compressed disk bytes = 0, want non-zero")
	}
}

func TestInspectRemoteImageBaseChainAuditOK(t *testing.T) {
	baseManifest, _, _ := pullCompressedChunkedTestManifest(t, []byte("abcdefghijklmnop"), 4)
	_, baseDigest := pullTestManifestData(t, baseManifest)
	manifest := baseManifest
	manifest.Annotations = cloneStringMap(manifest.Annotations)
	manifest.Annotations[ociimage.CoveBaseManifest] = baseDigest
	srv := newRemoteInspectManifestRegistry(t, manifest, map[string]ociimage.Manifest{
		baseDigest: baseManifest,
	})
	t.Cleanup(srv.Close)

	out, err := InspectRemoteImage(context.Background(), "ghcr.io/me/dev-vm:v1", remoteInspectOptions{RegistryBaseURL: srv.URL})
	if err != nil {
		t.Fatalf("InspectRemoteImage: %v", err)
	}
	if out.BaseManifest != baseDigest {
		t.Fatalf("base manifest = %q, want %q", out.BaseManifest, baseDigest)
	}
	if out.BaseChainAudit != "ok" || out.BaseChainDepth != 1 || len(out.BaseChain) != 1 {
		t.Fatalf("base audit = %q depth=%d chain=%+v, want one ok entry", out.BaseChainAudit, out.BaseChainDepth, out.BaseChain)
	}
	base := out.BaseChain[0]
	if base.Digest != baseDigest || base.Status != "ok" || base.Format != "cove" || base.MatchingChunks == 0 {
		t.Fatalf("base entry = %+v, want cove ok with matching chunks", base)
	}
}

func TestInspectRemoteImageBaseChainAuditMissing(t *testing.T) {
	manifest, _, _ := pullCompressedChunkedTestManifest(t, []byte("abcdefghijklmnop"), 4)
	missingDigest := "sha256:" + strings.Repeat("f", 64)
	manifest.Annotations = cloneStringMap(manifest.Annotations)
	manifest.Annotations[ociimage.CoveBaseManifest] = missingDigest
	srv := newRemoteInspectManifestRegistry(t, manifest, nil)
	t.Cleanup(srv.Close)

	out, err := InspectRemoteImage(context.Background(), "ghcr.io/me/dev-vm:v1", remoteInspectOptions{RegistryBaseURL: srv.URL})
	if err != nil {
		t.Fatalf("InspectRemoteImage: %v", err)
	}
	if out.BaseChainAudit != "missing" || len(out.BaseChain) != 1 {
		t.Fatalf("base audit = %q chain=%+v, want missing", out.BaseChainAudit, out.BaseChain)
	}
	if out.BaseChain[0].Digest != missingDigest || out.BaseChain[0].Status != "missing" {
		t.Fatalf("base entry = %+v, want missing digest %s", out.BaseChain[0], missingDigest)
	}
}

func TestInspectRemoteImageBlobAuditMissing(t *testing.T) {
	manifest, _, _ := pullCompressedChunkedTestManifest(t, []byte("abcdefghijklmnop"), 4)
	srv := newOCIDispatchRegistry(t, manifest)
	t.Cleanup(srv.Close)

	out, err := InspectRemoteImage(context.Background(), "ghcr.io/me/dev-vm:v1", remoteInspectOptions{
		RegistryBaseURL: srv.URL,
		VerifyBlobs:     true,
	})
	if err != nil {
		t.Fatalf("InspectRemoteImage: %v", err)
	}
	if out.BlobAudit != "missing" || out.BlobDescriptors == 0 || len(out.MissingBlobs) == 0 {
		t.Fatalf("blob audit = status:%q descriptors:%d missing:%v", out.BlobAudit, out.BlobDescriptors, out.MissingBlobs)
	}
}

func TestInspectRemoteImagesBatchReportsErrors(t *testing.T) {
	manifest, _, _ := pullCompressedChunkedTestManifest(t, []byte("abcdefghijklmnop"), 4)
	srv := newOCIDispatchRegistry(t, manifest)
	t.Cleanup(srv.Close)

	out, err := InspectRemoteImages(context.Background(), []string{
		"ghcr.io/me/dev-vm:v1",
		"ghcr.io/me/dev-vm",
	}, remoteInspectOptions{RegistryBaseURL: srv.URL})
	if err == nil || !strings.Contains(err.Error(), "1 of 2 refs failed") {
		t.Fatalf("InspectRemoteImages error = %v, want failed summary", err)
	}
	if len(out) != 2 {
		t.Fatalf("outputs = %d, want 2", len(out))
	}
	if out[0].Format != "cove" || out[0].Error != "" {
		t.Fatalf("first output = %+v, want cove success", out[0])
	}
	if out[1].Ref != "ghcr.io/me/dev-vm" || !strings.Contains(out[1].Error, "must include a tag or digest") {
		t.Fatalf("second output = %+v, want ref error", out[1])
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
	if !out.ResolvedFromIndex || out.IndexDigest != "sha256:index" || out.SelectedDigest != manifestDigest || out.SelectedPlatform != "darwin/arm64" {
		t.Fatalf("resolution = index:%v index_digest:%q selected:%q platform:%q", out.ResolvedFromIndex, out.IndexDigest, out.SelectedDigest, out.SelectedPlatform)
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
	if out.PullPlan != "lume tar-split import" || !strings.Contains(out.Verification, "part size/digest") {
		t.Fatalf("pull/verification = %q/%q", out.PullPlan, out.Verification)
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
	if out.PullPlan != "tart-compatible import" || !strings.Contains(out.Verification, "uncompressed disk digest") {
		t.Fatalf("pull/verification = %q/%q", out.PullPlan, out.Verification)
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
		DiskFormat:    "ASIF",
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
	if out.DiskFormat != "asif" {
		t.Fatalf("disk format = %q, want asif", out.DiskFormat)
	}
	if out.PullPlan != "cove image-store artifact" || !strings.Contains(out.Verification, "metadata blob size/digest verified") {
		t.Fatalf("pull/verification = %q/%q", out.PullPlan, out.Verification)
	}
	if out.CompressedDiskBytes != 42 || out.MetadataBlobs != 1 || out.MetadataBytes != 7 {
		t.Fatalf("compressed/meta = %d/%d/%d, want 42/1/7", out.CompressedDiskBytes, out.MetadataBlobs, out.MetadataBytes)
	}
}

func TestInspectRemoteImageBlobAuditOK(t *testing.T) {
	created := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	config := imagestore.Manifest{
		SchemaVersion: 1,
		Name:          "runner",
		Tag:           "v1",
		DiskSHA256:    strings.Repeat("a", 64),
		DiskSize:      99,
		CreatedAt:     created,
	}
	configBytes, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	configDigest := digestData(configBytes)
	diskData := []byte("disk")
	auxData := []byte("aux")
	diskDigest := digestData(diskData)
	auxDigest := digestData(auxData)
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
				Size:        int64(len(diskData)),
				Digest:      diskDigest,
				Annotations: map[string]string{ociTitleAnnotation: "disk.img.gz"},
			},
			{
				MediaType:   coveImageFileType,
				Size:        int64(len(auxData)),
				Digest:      auxDigest,
				Annotations: map[string]string{ociTitleAnnotation: "aux.img"},
			},
		},
	}
	srv := newRemoteInspectRegistry(t, manifest, map[string][]byte{
		configDigest: configBytes,
		diskDigest:   diskData,
		auxDigest:    auxData,
	})
	t.Cleanup(srv.Close)

	out, err := InspectRemoteImage(context.Background(), "ghcr.io/me/dev-vm:v1", remoteInspectOptions{
		RegistryBaseURL: srv.URL,
		VerifyBlobs:     true,
	})
	if err != nil {
		t.Fatalf("InspectRemoteImage: %v", err)
	}
	if out.BlobAudit != "ok" || out.BlobDescriptors != 3 || len(out.MissingBlobs) != 0 {
		t.Fatalf("blob audit = status:%q descriptors:%d missing:%v", out.BlobAudit, out.BlobDescriptors, out.MissingBlobs)
	}
	wantBytes := int64(len(configBytes) + len(diskData) + len(auxData))
	if out.BlobBytes != wantBytes {
		t.Fatalf("blob bytes = %d, want %d", out.BlobBytes, wantBytes)
	}
}

func TestWriteRemoteInspectText(t *testing.T) {
	var b strings.Builder
	err := writeRemoteInspectText(&b, ImageRemoteInspectOutput{
		Ref:                 "ghcr.io/me/dev-vm:v1",
		ManifestDigest:      "sha256:test",
		Kind:                "vm-oci",
		Format:              "cove",
		PullPlan:            "cove chunked pull",
		Verification:        "manifest parsed; compressed and uncompressed chunk digests verified during pull",
		BlobAudit:           "missing",
		BlobDescriptors:     2,
		BlobBytes:           9,
		MissingBlobs:        []string{"layer[0]:sha256:missing"},
		DiskSize:            16,
		DiskFormat:          "raw",
		CompressedDiskBytes: 8,
		ChunkCount:          2,
		LayerCount:          3,
		TotalLayerBytes:     9,
		BaseManifest:        "sha256:" + strings.Repeat("1", 64),
		BaseChainAudit:      "ok",
		BaseChainDepth:      1,
		BaseChain: []ImageRemoteBaseManifest{{
			Digest:         "sha256:" + strings.Repeat("1", 64),
			Status:         "ok",
			Format:         "cove",
			MatchingChunks: 2,
		}},
	})
	if err != nil {
		t.Fatalf("writeRemoteInspectText: %v", err)
	}
	for _, want := range []string{"Remote image ghcr.io/me/dev-vm:v1", "format:          cove", "pull plan:       cove chunked pull", "verification:    manifest parsed", "blob audit:      missing", "missing:       layer[0]:sha256:missing", "disk format:     raw", "chunks:          2", "base audit:      ok", "matching_chunks=2"} {
		if !strings.Contains(b.String(), want) {
			t.Fatalf("text missing %q:\n%s", want, b.String())
		}
	}
}

func TestWriteRemoteInspectBatchJSON(t *testing.T) {
	var b strings.Builder
	err := writeRemoteInspectJSONList(&b, []ImageRemoteInspectOutput{
		{Ref: "ghcr.io/me/a:v1", Kind: "vm-oci", Format: "cove"},
		{Ref: "ghcr.io/me/b", Error: "missing tag"},
	})
	if err != nil {
		t.Fatalf("writeRemoteInspectJSONList: %v", err)
	}
	var out []ImageRemoteInspectOutput
	if err := json.Unmarshal([]byte(b.String()), &out); err != nil {
		t.Fatalf("unmarshal batch json: %v\n%s", err, b.String())
	}
	if len(out) != 2 || out[0].Format != "cove" || out[1].Error != "missing tag" {
		t.Fatalf("batch json = %+v", out)
	}
}

func TestWriteRemoteInspectBatchText(t *testing.T) {
	var b strings.Builder
	err := writeRemoteInspectTextList(&b, []ImageRemoteInspectOutput{
		{Ref: "ghcr.io/me/a:v1", Kind: "vm-oci", Format: "cove"},
		{Ref: "ghcr.io/me/b", Error: "missing tag"},
	})
	if err != nil {
		t.Fatalf("writeRemoteInspectTextList: %v", err)
	}
	text := b.String()
	for _, want := range []string{"Remote image ghcr.io/me/a:v1", "format:          cove", "Remote image ghcr.io/me/b", "error:           missing tag"} {
		if !strings.Contains(text, want) {
			t.Fatalf("batch text missing %q:\n%s", want, text)
		}
	}
}

func TestMoveImageInspectFlagsFirst(t *testing.T) {
	got := strings.Join(moveImageInspectFlagsFirst([]string{
		"registry.example.com/team/vm:v1",
		"-remote",
		"-json",
		"-verify-blobs",
	}), " ")
	want := "-remote -json -verify-blobs registry.example.com/team/vm:v1"
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

func newRemoteInspectManifestRegistry(t *testing.T, tagManifest ociimage.Manifest, byDigest map[string]ociimage.Manifest) *httptest.Server {
	t.Helper()
	tagData, tagDigest := pullTestManifestData(t, tagManifest)
	digests := map[string][]byte{}
	for digest, manifest := range byDigest {
		data, _ := pullTestManifestData(t, manifest)
		digests[digest] = data
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/me/dev-vm/manifests/v1":
			w.Header().Set("Content-Type", ociimage.MediaTypeImageManifest)
			w.Header().Set("Docker-Content-Digest", tagDigest)
			_, _ = w.Write(tagData)
		case strings.HasPrefix(r.URL.Path, "/v2/me/dev-vm/manifests/"):
			digest := strings.TrimPrefix(r.URL.Path, "/v2/me/dev-vm/manifests/")
			data, ok := digests[digest]
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", ociimage.MediaTypeImageManifest)
			w.Header().Set("Docker-Content-Digest", digest)
			_, _ = w.Write(data)
		default:
			http.NotFound(w, r)
		}
	}))
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
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
