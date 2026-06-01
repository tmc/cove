package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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
	if out.DigestRef != "ghcr.io/me/dev-vm@sha256:dispatch-test" {
		t.Fatalf("digest ref = %q, want selected digest ref", out.DigestRef)
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

func TestInspectRemoteImageManifestOut(t *testing.T) {
	manifest, _, _ := pullCompressedChunkedTestManifest(t, []byte("abcdefghijklmnop"), 4)
	manifestData, manifestDigest := pullTestManifestData(t, manifest)
	srv := newRemoteInspectRegistry(t, manifest, nil)
	t.Cleanup(srv.Close)

	out, err := InspectRemoteImage(context.Background(), "ghcr.io/me/dev-vm:v1", remoteInspectOptions{RegistryBaseURL: srv.URL})
	if err != nil {
		t.Fatalf("InspectRemoteImage: %v", err)
	}
	if out.ManifestDigest != manifestDigest || string(out.manifestRaw) != string(manifestData) {
		t.Fatalf("manifest = digest:%q raw:%q, want digest %q raw %q", out.ManifestDigest, string(out.manifestRaw), manifestDigest, string(manifestData))
	}
	path := filepath.Join(t.TempDir(), "selected-manifest.json")
	if err := writeRemoteInspectManifestOut(out, path); err != nil {
		t.Fatalf("writeRemoteInspectManifestOut: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest-out: %v", err)
	}
	if string(got) != string(manifestData) || digestData(got) != manifestDigest {
		t.Fatalf("manifest-out = %q digest=%s, want registry bytes digest %s", string(got), digestData(got), manifestDigest)
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
	if base.Digest != baseDigest || base.Status != "ok" || base.Format != "cove" || base.DiskFormat != "raw" || base.MatchingChunks != 4 || base.MatchingBytes != 16 {
		t.Fatalf("base entry = %+v, want cove ok with matching chunks", base)
	}
}

func TestInspectRemoteImageBaseChainAuditDiskFormatMismatch(t *testing.T) {
	baseManifest, _, _ := pullCompressedChunkedTestManifest(t, []byte("abcdefghijklmnop"), 4)
	_, baseDigest := pullTestManifestData(t, baseManifest)
	manifest := baseManifest
	manifest.Annotations = cloneStringMap(manifest.Annotations)
	manifest.Annotations[ociimage.CoveDiskFormat] = "asif"
	manifest.Annotations[ociimage.CoveBaseManifest] = baseDigest
	srv := newRemoteInspectManifestRegistry(t, manifest, map[string]ociimage.Manifest{
		baseDigest: baseManifest,
	})
	t.Cleanup(srv.Close)

	out, err := InspectRemoteImage(context.Background(), "ghcr.io/me/dev-vm:v1", remoteInspectOptions{RegistryBaseURL: srv.URL})
	if err != nil {
		t.Fatalf("InspectRemoteImage: %v", err)
	}
	if out.DiskFormat != "asif" {
		t.Fatalf("child disk format = %q, want asif", out.DiskFormat)
	}
	if out.BaseChainAudit != "incompatible" || len(out.BaseChain) != 1 {
		t.Fatalf("base audit = %q chain=%+v, want incompatible", out.BaseChainAudit, out.BaseChain)
	}
	base := out.BaseChain[0]
	if base.Status != "incompatible" || base.DiskFormat != "raw" || !strings.Contains(base.Error, "disk format raw, child asif") {
		t.Fatalf("base entry = %+v, want raw/asif incompatibility", base)
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
	indexData := remoteInspectIndexData(t, index)

	out, err := InspectRemoteImage(context.Background(), "ghcr.io/me/dev-vm:v1", remoteInspectOptions{RegistryBaseURL: srv.URL})
	if err != nil {
		t.Fatalf("InspectRemoteImage: %v", err)
	}
	if out.ManifestDigest != manifestDigest {
		t.Fatalf("manifest digest = %q, want %q", out.ManifestDigest, manifestDigest)
	}
	if out.DigestRef != "ghcr.io/me/dev-vm@"+manifestDigest || out.IndexDigestRef != "ghcr.io/me/dev-vm@sha256:index" {
		t.Fatalf("digest refs = selected:%q index:%q, want child and index refs", out.DigestRef, out.IndexDigestRef)
	}
	if string(out.indexRaw) != string(indexData) {
		t.Fatalf("index raw = %q, want registry index bytes %q", string(out.indexRaw), string(indexData))
	}
	indexOut := filepath.Join(t.TempDir(), "index.json")
	if err := writeRemoteInspectIndexOut(out, indexOut); err != nil {
		t.Fatalf("writeRemoteInspectIndexOut: %v", err)
	}
	gotIndex, err := os.ReadFile(indexOut)
	if err != nil {
		t.Fatalf("read index-out: %v", err)
	}
	if string(gotIndex) != string(indexData) || out.IndexDigest != "sha256:index" {
		t.Fatalf("index-out = %q recorded_digest=%s, want registry index bytes recorded as sha256:index", string(gotIndex), out.IndexDigest)
	}
	if !out.ResolvedFromIndex || out.IndexDigest != "sha256:index" || out.SelectedDigest != manifestDigest || out.SelectedPlatform != "darwin/arm64" {
		t.Fatalf("resolution = index:%v index_digest:%q selected:%q platform:%q", out.ResolvedFromIndex, out.IndexDigest, out.SelectedDigest, out.SelectedPlatform)
	}
	if len(out.IndexManifests) != 2 || out.IndexManifests[1].Digest != manifestDigest || !out.IndexManifests[1].Selected {
		t.Fatalf("index manifests = %+v, want selected darwin candidate", out.IndexManifests)
	}
	if out.Format != "cove" || out.DiskSize != 16 {
		t.Fatalf("format/disk = %s/%d, want cove/16", out.Format, out.DiskSize)
	}
}

func TestInspectRemoteImageResolvesIndexPlatform(t *testing.T) {
	darwinManifest, _, _ := pullCompressedChunkedTestManifest(t, []byte("darwin"), 3)
	linuxManifest, _, _ := pullCompressedChunkedTestManifest(t, []byte("linux-child"), 5)
	darwinData, darwinDigest := pullTestManifestData(t, darwinManifest)
	linuxData, linuxDigest := pullTestManifestData(t, linuxManifest)
	index := ociimage.Index{
		SchemaVersion: 2,
		MediaType:     ociimage.MediaTypeImageIndex,
		Manifests: []ociimage.IndexDescriptor{
			{
				Descriptor: ociimage.Descriptor{MediaType: ociimage.MediaTypeImageManifest, Size: int64(len(darwinData)), Digest: darwinDigest},
				Platform:   &ociimage.Platform{OS: "darwin", Architecture: "arm64"},
			},
			{
				Descriptor: ociimage.Descriptor{MediaType: ociimage.MediaTypeImageManifest, Size: int64(len(linuxData)), Digest: linuxDigest},
				Platform:   &ociimage.Platform{OS: "linux", Architecture: "arm64"},
			},
		},
	}
	srv := newRemoteInspectMultiIndexRegistry(t, index, map[string][]byte{
		darwinDigest: darwinData,
		linuxDigest:  linuxData,
	})
	t.Cleanup(srv.Close)

	out, err := InspectRemoteImage(context.Background(), "ghcr.io/me/dev-vm:v1", remoteInspectOptions{
		RegistryBaseURL:       srv.URL,
		Platform:              "linux/arm64",
		InspectIndexManifests: true,
	})
	if err != nil {
		t.Fatalf("InspectRemoteImage: %v", err)
	}
	if out.ManifestDigest != linuxDigest || out.SelectedDigest != linuxDigest {
		t.Fatalf("digest = manifest:%q selected:%q, want linux %q", out.ManifestDigest, out.SelectedDigest, linuxDigest)
	}
	if out.DigestRef != "ghcr.io/me/dev-vm@"+linuxDigest || out.IndexDigestRef != "ghcr.io/me/dev-vm@sha256:index" {
		t.Fatalf("digest refs = selected:%q index:%q, want linux child and index refs", out.DigestRef, out.IndexDigestRef)
	}
	if !out.ResolvedFromIndex || out.SelectedPlatform != "linux/arm64" {
		t.Fatalf("resolution = index:%v platform:%q, want linux/arm64", out.ResolvedFromIndex, out.SelectedPlatform)
	}
	if len(out.IndexManifests) != 2 || out.IndexManifests[0].Platform != "darwin/arm64" || out.IndexManifests[1].Platform != "linux/arm64" || !out.IndexManifests[1].Selected {
		t.Fatalf("index manifests = %+v, want darwin and selected linux candidates", out.IndexManifests)
	}
	if out.IndexManifests[0].Format != "cove" || out.IndexManifests[0].DiskSize != int64(len("darwin")) || out.IndexManifests[1].Format != "cove" || out.IndexManifests[1].DiskSize != int64(len("linux-child")) {
		t.Fatalf("index manifest details = %+v, want cove summaries for both children", out.IndexManifests)
	}
	if out.DiskSize != int64(len("linux-child")) {
		t.Fatalf("disk size = %d, want linux child", out.DiskSize)
	}
}

func TestInspectRemoteImageAllPlatformsBaseChainAudit(t *testing.T) {
	darwinBase, _, _ := pullCompressedChunkedTestManifest(t, []byte("darwin"), 3)
	darwinBaseData, darwinBaseDigest := pullTestManifestData(t, darwinBase)
	darwinManifest := darwinBase
	darwinManifest.Annotations = cloneStringMap(darwinManifest.Annotations)
	darwinManifest.Annotations[ociimage.CoveBaseManifest] = darwinBaseDigest
	linuxManifest, _, _ := pullCompressedChunkedTestManifest(t, []byte("linux-child"), 5)
	missingBaseDigest := "sha256:" + strings.Repeat("f", 64)
	linuxManifest.Annotations = cloneStringMap(linuxManifest.Annotations)
	linuxManifest.Annotations[ociimage.CoveBaseManifest] = missingBaseDigest
	darwinData, darwinDigest := pullTestManifestData(t, darwinManifest)
	linuxData, linuxDigest := pullTestManifestData(t, linuxManifest)
	index := ociimage.Index{
		SchemaVersion: 2,
		MediaType:     ociimage.MediaTypeImageIndex,
		Manifests: []ociimage.IndexDescriptor{
			{
				Descriptor: ociimage.Descriptor{MediaType: ociimage.MediaTypeImageManifest, Size: int64(len(darwinData)), Digest: darwinDigest},
				Platform:   &ociimage.Platform{OS: "darwin", Architecture: "arm64"},
			},
			{
				Descriptor: ociimage.Descriptor{MediaType: ociimage.MediaTypeImageManifest, Size: int64(len(linuxData)), Digest: linuxDigest},
				Platform:   &ociimage.Platform{OS: "linux", Architecture: "arm64"},
			},
		},
	}
	srv := newRemoteInspectMultiIndexRegistry(t, index, map[string][]byte{
		darwinDigest:     darwinData,
		darwinBaseDigest: darwinBaseData,
		linuxDigest:      linuxData,
	})
	t.Cleanup(srv.Close)

	out, err := InspectRemoteImage(context.Background(), "ghcr.io/me/dev-vm:v1", remoteInspectOptions{
		RegistryBaseURL:       srv.URL,
		Platform:              "linux/arm64",
		InspectIndexManifests: true,
	})
	if err != nil {
		t.Fatalf("InspectRemoteImage: %v", err)
	}
	if out.BaseChainAudit != "missing" || out.BaseChainDepth != 1 {
		t.Fatalf("selected base audit = %q depth=%d chain=%+v, want missing depth 1", out.BaseChainAudit, out.BaseChainDepth, out.BaseChain)
	}
	children := map[string]ImageRemoteIndexManifest{}
	for _, child := range out.IndexManifests {
		children[child.Platform] = child
	}
	darwin := children["darwin/arm64"]
	if darwin.BaseManifest != darwinBaseDigest || darwin.BaseChainAudit != "ok" || darwin.BaseChainDepth != 1 || len(darwin.BaseChain) != 1 {
		t.Fatalf("darwin child base audit = manifest:%q status:%q depth:%d chain:%+v, want one ok parent", darwin.BaseManifest, darwin.BaseChainAudit, darwin.BaseChainDepth, darwin.BaseChain)
	}
	if darwin.BaseChain[0].Digest != darwinBaseDigest || darwin.BaseChain[0].Status != "ok" || darwin.BaseChain[0].MatchingBytes != int64(len("darwin")) {
		t.Fatalf("darwin base entry = %+v, want reusable parent", darwin.BaseChain[0])
	}
	linux := children["linux/arm64"]
	if linux.BaseManifest != missingBaseDigest || linux.BaseChainAudit != "missing" || linux.BaseChainDepth != 1 || len(linux.BaseChain) != 1 {
		t.Fatalf("linux child base audit = manifest:%q status:%q depth:%d chain:%+v, want missing parent", linux.BaseManifest, linux.BaseChainAudit, linux.BaseChainDepth, linux.BaseChain)
	}
	if linux.BaseChain[0].Digest != missingBaseDigest || linux.BaseChain[0].Status != "missing" {
		t.Fatalf("linux base entry = %+v, want missing parent %s", linux.BaseChain[0], missingBaseDigest)
	}
}

func TestInspectRemoteImageAllPlatformsVerifyBlobs(t *testing.T) {
	darwinManifest, _, _ := pullCompressedChunkedTestManifest(t, []byte("darwin"), 3)
	linuxManifest, _, _ := pullCompressedChunkedTestManifest(t, []byte("linux-child"), 5)
	setRemoteInspectTestConfig(&darwinManifest, []byte("darwin-config"))
	setRemoteInspectTestConfig(&linuxManifest, []byte("linux-config"))
	darwinData, darwinDigest := pullTestManifestData(t, darwinManifest)
	linuxData, linuxDigest := pullTestManifestData(t, linuxManifest)
	index := ociimage.Index{
		SchemaVersion: 2,
		MediaType:     ociimage.MediaTypeImageIndex,
		Manifests: []ociimage.IndexDescriptor{
			{
				Descriptor: ociimage.Descriptor{MediaType: ociimage.MediaTypeImageManifest, Size: int64(len(darwinData)), Digest: darwinDigest},
				Platform:   &ociimage.Platform{OS: "darwin", Architecture: "arm64"},
			},
			{
				Descriptor: ociimage.Descriptor{MediaType: ociimage.MediaTypeImageManifest, Size: int64(len(linuxData)), Digest: linuxDigest},
				Platform:   &ociimage.Platform{OS: "linux", Architecture: "arm64"},
			},
		},
	}
	blobs := remoteInspectExistingBlobs(darwinManifest, linuxManifest)
	srv := newRemoteInspectMultiIndexBlobRegistry(t, index, map[string][]byte{
		darwinDigest: darwinData,
		linuxDigest:  linuxData,
	}, blobs)
	t.Cleanup(srv.Close)

	out, err := InspectRemoteImage(context.Background(), "ghcr.io/me/dev-vm:v1", remoteInspectOptions{
		RegistryBaseURL:       srv.URL,
		Platform:              "linux/arm64",
		InspectIndexManifests: true,
		VerifyBlobs:           true,
	})
	if err != nil {
		t.Fatalf("InspectRemoteImage: %v", err)
	}
	if out.BlobAudit != "ok" || out.BlobDescriptors != len(remoteBlobDescriptors(linuxManifest)) {
		t.Fatalf("selected blob audit = %q/%d, want ok/%d", out.BlobAudit, out.BlobDescriptors, len(remoteBlobDescriptors(linuxManifest)))
	}
	manifests := map[string]ociimage.Manifest{
		darwinDigest: darwinManifest,
		linuxDigest:  linuxManifest,
	}
	if len(out.IndexManifests) != 2 {
		t.Fatalf("index manifests = %d, want 2", len(out.IndexManifests))
	}
	for _, child := range out.IndexManifests {
		manifest, ok := manifests[child.Digest]
		if !ok {
			t.Fatalf("unexpected child digest %s", child.Digest)
		}
		wantDescriptors := len(remoteBlobDescriptors(manifest))
		if child.BlobAudit != "ok" || child.BlobDescriptors != wantDescriptors || len(child.MissingBlobs) != 0 {
			t.Fatalf("child %s audit = status:%q descriptors:%d missing:%v, want ok/%d", child.Platform, child.BlobAudit, child.BlobDescriptors, child.MissingBlobs, wantDescriptors)
		}
	}
	wantHeads := len(remoteBlobDescriptors(darwinManifest)) + 2*len(remoteBlobDescriptors(linuxManifest))
	if got := int(srv.blobHeads.Load()); got != wantHeads {
		t.Fatalf("blob HEADs = %d, want %d", got, wantHeads)
	}
	if got := srv.blobGets.Load(); got != 0 {
		t.Fatalf("blob GETs = %d, want 0", got)
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
		Ref:               "ghcr.io/me/dev-vm:v1",
		ManifestDigest:    "sha256:test",
		DigestRef:         "ghcr.io/me/dev-vm@sha256:test",
		ResolvedFromIndex: true,
		IndexDigest:       "sha256:index",
		IndexDigestRef:    "ghcr.io/me/dev-vm@sha256:index",
		IndexMediaType:    ociimage.MediaTypeImageIndex,
		SelectedDigest:    "sha256:" + strings.Repeat("2", 64),
		SelectedPlatform:  "darwin/arm64",
		IndexManifests: []ImageRemoteIndexManifest{
			{
				Digest:              "sha256:" + strings.Repeat("1", 64),
				MediaType:           ociimage.MediaTypeImageManifest,
				Size:                1024,
				Platform:            "linux/arm64",
				Format:              "cove",
				DiskSize:            4096,
				DiskFormat:          "raw",
				CompressedDiskBytes: 128,
				ChunkCount:          1,
				BaseManifest:        "sha256:" + strings.Repeat("3", 64),
				BaseChainAudit:      "ok",
				BaseChainDepth:      1,
				BaseChain: []ImageRemoteBaseManifest{{
					Digest:         "sha256:" + strings.Repeat("4", 64),
					Status:         "ok",
					Format:         "cove",
					DiskFormat:     "raw",
					MatchingChunks: 1,
					MatchingBytes:  4096,
				}},
				BlobAudit:       "ok",
				BlobDescriptors: 2,
				BlobBytes:       512,
			},
			{Digest: "sha256:" + strings.Repeat("2", 64), MediaType: ociimage.MediaTypeImageManifest, Size: 2048, Platform: "darwin/arm64", Selected: true, Format: "tart", DiskSize: 8192, CompressedDiskBytes: 256, ChunkCount: 2, BlobAudit: "missing", BlobDescriptors: 3, MissingBlobs: []string{"layer[1]:sha256:missing"}},
		},
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
			DiskFormat:     "raw",
			MatchingChunks: 2,
			MatchingBytes:  8192,
		}},
	})
	if err != nil {
		t.Fatalf("writeRemoteInspectText: %v", err)
	}
	for _, want := range []string{"Remote image ghcr.io/me/dev-vm:v1", "digest ref:      ghcr.io/me/dev-vm@sha256:test", "index ref:       ghcr.io/me/dev-vm@sha256:index", "format:          cove", "pull plan:       cove chunked pull", "verification:    manifest parsed", "index manifests: 2", "platform=darwin/arm64", "size=2.0 KB", "format=tart", "disk_size=8.0 KB", "transport=256 B", "base_manifest=sha256:3333", "base_audit=ok", "base_depth=1", "base: sha256:4444", "matching_chunks=1", "matching_bytes=4.0 KB", "blob_audit=ok", "blobs=2", "blob_bytes=512 B", "blob_audit=missing", "missing=1", "missing: layer[1]:sha256:missing", "blob audit:      missing", "missing:       layer[0]:sha256:missing", "disk format:     raw", "chunks:          2", "base audit:      ok", "disk_format=raw", "matching_chunks=2", "matching_bytes=8.0 KB"} {
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
		"-all-platforms",
		"-manifest-out", "manifest.json",
		"-index-out", "index.json",
		"-platform", "linux/arm64",
	}), " ")
	want := "-remote -json -verify-blobs -all-platforms -manifest-out manifest.json -index-out index.json -platform linux/arm64 registry.example.com/team/vm:v1"
	if got != want {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

func TestRunImageInspectAllPlatformsRequiresRemote(t *testing.T) {
	err := runImageInspect(imageTestEnv(), []string{"-all-platforms", "local:latest"})
	if err == nil || !strings.Contains(err.Error(), "-all-platforms requires -remote") {
		t.Fatalf("runImageInspect() error = %v, want -all-platforms requires -remote", err)
	}
}

func TestRunImageInspectManifestOutRequiresRemote(t *testing.T) {
	err := runImageInspect(imageTestEnv(), []string{"-manifest-out", "manifest.json", "local:latest"})
	if err == nil || !strings.Contains(err.Error(), "-manifest-out requires -remote") {
		t.Fatalf("runImageInspect() error = %v, want -manifest-out requires -remote", err)
	}
}

func TestRunImageInspectManifestOutRejectsBatch(t *testing.T) {
	err := runImageInspect(imageTestEnv(), []string{"-remote", "-manifest-out", "manifest.json", "registry.example.com/team/a:v1", "registry.example.com/team/b:v1"})
	if err == nil || !strings.Contains(err.Error(), "-manifest-out requires exactly one remote ref") {
		t.Fatalf("runImageInspect() error = %v, want single-ref manifest-out error", err)
	}
}

func TestRunImageInspectIndexOutRequiresRemote(t *testing.T) {
	err := runImageInspect(imageTestEnv(), []string{"-index-out", "index.json", "local:latest"})
	if err == nil || !strings.Contains(err.Error(), "-index-out requires -remote") {
		t.Fatalf("runImageInspect() error = %v, want -index-out requires -remote", err)
	}
}

func TestRunImageInspectIndexOutRejectsBatch(t *testing.T) {
	err := runImageInspect(imageTestEnv(), []string{"-remote", "-index-out", "index.json", "registry.example.com/team/a:v1", "registry.example.com/team/b:v1"})
	if err == nil || !strings.Contains(err.Error(), "-index-out requires exactly one remote ref") {
		t.Fatalf("runImageInspect() error = %v, want single-ref index-out error", err)
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
	indexData := remoteInspectIndexData(t, index)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/me/dev-vm/manifests/v1":
			w.Header().Set("Content-Type", ociimage.MediaTypeImageIndex)
			w.Header().Set("Docker-Content-Digest", "sha256:index")
			_, _ = w.Write(indexData)
		case "/v2/me/dev-vm/manifests/" + manifestDigest:
			w.Header().Set("Content-Type", ociimage.MediaTypeImageManifest)
			w.Header().Set("Docker-Content-Digest", manifestDigest)
			_, _ = w.Write(manifestData)
		default:
			http.NotFound(w, r)
		}
	}))
}

func newRemoteInspectMultiIndexRegistry(t *testing.T, index ociimage.Index, manifests map[string][]byte) *httptest.Server {
	t.Helper()
	indexData := remoteInspectIndexData(t, index)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/me/dev-vm/manifests/v1":
			w.Header().Set("Content-Type", ociimage.MediaTypeImageIndex)
			w.Header().Set("Docker-Content-Digest", "sha256:index")
			_, _ = w.Write(indexData)
		case strings.HasPrefix(r.URL.Path, "/v2/me/dev-vm/manifests/"):
			digest := strings.TrimPrefix(r.URL.Path, "/v2/me/dev-vm/manifests/")
			data, ok := manifests[digest]
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

type remoteInspectMultiIndexBlobRegistry struct {
	*httptest.Server
	blobHeads atomic.Int64
	blobGets  atomic.Int64
}

func remoteInspectIndexData(t *testing.T, index ociimage.Index) []byte {
	t.Helper()
	data, err := json.Marshal(index)
	if err != nil {
		t.Fatalf("Marshal(index): %v", err)
	}
	return append(data, '\n')
}

func newRemoteInspectMultiIndexBlobRegistry(t *testing.T, index ociimage.Index, manifests map[string][]byte, blobs map[string]bool) *remoteInspectMultiIndexBlobRegistry {
	t.Helper()
	srv := &remoteInspectMultiIndexBlobRegistry{}
	indexData := remoteInspectIndexData(t, index)
	srv.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/me/dev-vm/manifests/v1":
			w.Header().Set("Content-Type", ociimage.MediaTypeImageIndex)
			w.Header().Set("Docker-Content-Digest", "sha256:index")
			_, _ = w.Write(indexData)
		case strings.HasPrefix(r.URL.Path, "/v2/me/dev-vm/manifests/"):
			digest := strings.TrimPrefix(r.URL.Path, "/v2/me/dev-vm/manifests/")
			data, ok := manifests[digest]
			if !ok {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", ociimage.MediaTypeImageManifest)
			w.Header().Set("Docker-Content-Digest", digest)
			_, _ = w.Write(data)
		case strings.HasPrefix(r.URL.Path, "/v2/me/dev-vm/blobs/"):
			digest := strings.TrimPrefix(r.URL.Path, "/v2/me/dev-vm/blobs/")
			switch r.Method {
			case http.MethodHead:
				srv.blobHeads.Add(1)
				if !blobs[digest] {
					http.NotFound(w, r)
					return
				}
				w.WriteHeader(http.StatusOK)
			case http.MethodGet:
				srv.blobGets.Add(1)
				http.Error(w, "unexpected blob GET", http.StatusInternalServerError)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	return srv
}

func setRemoteInspectTestConfig(manifest *ociimage.Manifest, data []byte) {
	manifest.Config = ociimage.Descriptor{
		MediaType: ociimage.MediaTypeImageConfig,
		Size:      int64(len(data)),
		Digest:    pushTestDigest(data),
	}
}

func remoteInspectExistingBlobs(manifests ...ociimage.Manifest) map[string]bool {
	blobs := map[string]bool{}
	for _, manifest := range manifests {
		for _, desc := range remoteBlobDescriptors(manifest) {
			if desc.Descriptor.Digest != "" {
				blobs[desc.Descriptor.Digest] = true
			}
		}
	}
	return blobs
}
