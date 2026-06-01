package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tmc/cove/internal/diskimages2"
	"github.com/tmc/cove/internal/ociimage"
	"github.com/tmc/cove/internal/store"
)

func TestBuildPullPlanDryRunManifest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	manifestPath := writePullTestManifest(t)

	plan, err := buildPullPlan("ghcr.io/me/dev-vm:v1", pullOptions{
		As:           "local-dev",
		DryRun:       true,
		ManifestPath: manifestPath,
	})
	if err != nil {
		t.Fatalf("buildPullPlan(): %v", err)
	}
	if plan.VMName != "local-dev" {
		t.Fatalf("VMName = %q, want local-dev", plan.VMName)
	}
	if plan.Ref.String() != "ghcr.io/me/dev-vm:v1" {
		t.Fatalf("Ref = %q", plan.Ref.String())
	}
	if got, want := len(plan.Manifest.Chunks), 1; got != want {
		t.Fatalf("chunks = %d, want %d", got, want)
	}
	if plan.Manifest.Annotations.UncompressedDiskSize != 3 {
		t.Fatalf("disk size = %d, want 3", plan.Manifest.Annotations.UncompressedDiskSize)
	}
}

func TestBuildPullPlanDryRunReportsLocalBaseReuse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	baseDisk := []byte("aaaabbbb")
	targetDisk := []byte("aaaacccc")
	baseManifest, _, _ := pullCompressedChunkedTestManifest(t, baseDisk, 4)
	baseData, baseDigest := pullTestManifestData(t, baseManifest)
	if err := store.New("").StoreManifest(baseDigest, baseData); err != nil {
		t.Fatalf("StoreManifest(base): %v", err)
	}
	writePullBaseVM(t, home, "base", baseDigest, baseDisk)

	manifest, _, _ := pullCompressedChunkedTestManifest(t, targetDisk, 4)
	manifest.Annotations[ociimage.CoveBaseManifest] = baseDigest
	manifestPath := writePullManifest(t, manifest)

	plan, err := buildPullPlan("ghcr.io/me/dev-vm:v1", pullOptions{
		As:           "child",
		DryRun:       true,
		ManifestPath: manifestPath,
	})
	if err != nil {
		t.Fatalf("buildPullPlan(): %v", err)
	}
	if plan.BaseReusePath == "" || plan.BaseReuseDiskFormat != "raw" || plan.BaseReuseChunks != 1 || plan.BaseReuseBytes != 4 {
		t.Fatalf("base reuse summary = path:%q format:%q chunks:%d bytes:%d, want raw one 4-byte chunk", plan.BaseReusePath, plan.BaseReuseDiskFormat, plan.BaseReuseChunks, plan.BaseReuseBytes)
	}

	var out strings.Builder
	printPullDryRun(&out, plan)
	for _, want := range []string{"base reuse: 1 chunks", "4 B", "format=raw", "from=" + plan.BaseReusePath} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("dry-run output %q missing %q", out.String(), want)
		}
	}
}

func TestBuildPullPlanDryRunReportsTransferPreflight(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	baseDisk := []byte("aaaaxxxxddddzzzz")
	targetDisk := []byte("aaaabbbbcccc\x00\x00\x00\x00")
	baseManifest, _, _ := pullCompressedChunkedTestManifest(t, baseDisk, 4)
	baseData, baseDigest := pullTestManifestData(t, baseManifest)
	blobStore := store.New("")
	if err := blobStore.StoreManifest(baseDigest, baseData); err != nil {
		t.Fatalf("StoreManifest(base): %v", err)
	}
	writePullBaseVM(t, home, "base", baseDigest, baseDisk)

	manifest, blobs, _ := pullCompressedChunkedTestManifest(t, targetDisk, 4)
	manifest.Annotations[ociimage.CoveBaseManifest] = baseDigest
	parsed, err := ociimage.ParseManifest(manifest)
	if err != nil {
		t.Fatalf("ParseManifest(target): %v", err)
	}
	storeDisk := parsed.DiskLayers[1].Descriptor
	if err := blobStore.Put(storeDisk.Digest, storeDisk.Size, bytes.NewReader(blobs[storeDisk.Digest])); err != nil {
		t.Fatalf("Put(store disk): %v", err)
	}
	storeMetadata := parsed.Blobs[0]
	if err := blobStore.Put(storeMetadata.Digest, storeMetadata.Size, bytes.NewReader(blobs[storeMetadata.Digest])); err != nil {
		t.Fatalf("Put(store metadata): %v", err)
	}
	manifestPath := writePullManifest(t, manifest)

	plan, err := buildPullPlan("ghcr.io/me/dev-vm:v1", pullOptions{
		As:           "child",
		DryRun:       true,
		ManifestPath: manifestPath,
	})
	if err != nil {
		t.Fatalf("buildPullPlan(): %v", err)
	}
	if plan.BaseReuseChunks != 1 || plan.BaseReuseBytes != 4 {
		t.Fatalf("base reuse = (%d, %d), want (1, 4)", plan.BaseReuseChunks, plan.BaseReuseBytes)
	}
	if plan.StoreDiskChunks != 1 || plan.StoreDiskBytes != parsed.DiskLayers[1].Descriptor.Size {
		t.Fatalf("disk store reuse = (%d, %d), want (1, %d)", plan.StoreDiskChunks, plan.StoreDiskBytes, parsed.DiskLayers[1].Descriptor.Size)
	}
	if plan.FetchDiskChunks != 1 || plan.FetchDiskBytes != parsed.DiskLayers[2].Descriptor.Size {
		t.Fatalf("disk fetch = (%d, %d), want (1, %d)", plan.FetchDiskChunks, plan.FetchDiskBytes, parsed.DiskLayers[2].Descriptor.Size)
	}
	if plan.ZeroDiskChunks != 1 || plan.ZeroDiskBytes != 4 {
		t.Fatalf("zero chunks = (%d, %d), want (1, 4)", plan.ZeroDiskChunks, plan.ZeroDiskBytes)
	}
	if plan.StoreMetadataBlobs != 1 || plan.StoreMetadataBytes != parsed.Blobs[0].Size {
		t.Fatalf("metadata store reuse = (%d, %d), want (1, %d)", plan.StoreMetadataBlobs, plan.StoreMetadataBytes, parsed.Blobs[0].Size)
	}
	wantFetchMetadataBytes := parsed.Blobs[1].Size + parsed.Blobs[2].Size
	if plan.FetchMetadataBlobs != 2 || plan.FetchMetadataBytes != wantFetchMetadataBytes {
		t.Fatalf("metadata fetch = (%d, %d), want (2, %d)", plan.FetchMetadataBlobs, plan.FetchMetadataBytes, wantFetchMetadataBytes)
	}

	var out strings.Builder
	printPullDryRun(&out, plan)
	for _, want := range []string{
		"disk fetch: 1 chunks",
		"disk store reuse: 1 chunks",
		"zero chunks: 1 (4 B)",
		"metadata fetch: 2 blobs",
		"metadata store reuse: 1 blobs",
		"base reuse: 1 chunks",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("dry-run output %q missing %q", out.String(), want)
		}
	}

	var jsonOut strings.Builder
	if err := printPullDryRunJSON(&jsonOut, plan); err != nil {
		t.Fatalf("printPullDryRunJSON(): %v", err)
	}
	var got pullDryRunOutput
	if err := json.Unmarshal([]byte(jsonOut.String()), &got); err != nil {
		t.Fatalf("Unmarshal(JSON): %v\n%s", err, jsonOut.String())
	}
	if !got.ManifestProvided || got.Format != "cove" || got.DiskFormat != "raw" || got.Transfer == nil || got.BaseReuse == nil {
		t.Fatalf("JSON summary = %+v, want cove manifest with transfer and base reuse", got)
	}
	if got.Transfer.DiskFetchChunks != 1 || got.Transfer.DiskStoreChunks != 1 || got.Transfer.ZeroChunks != 1 || got.Transfer.MetadataFetchBlobs != 2 || got.Transfer.MetadataStoreBlobs != 1 {
		t.Fatalf("JSON transfer = %+v, want fetch/store/zero/metadata counts", got.Transfer)
	}
	if got.BaseReuse.Chunks != 1 || got.BaseReuse.Bytes != 4 || got.BaseReuse.Path == "" {
		t.Fatalf("JSON base reuse = %+v, want one reused chunk", got.BaseReuse)
	}
}

func TestBuildPullPlanDryRunWithoutManifestIsNetworkFree(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	plan, err := buildPullPlan("ghcr.io/me/dev-vm:v1", pullOptions{
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("buildPullPlan(): %v", err)
	}
	if plan.ManifestDigest != "" {
		t.Fatalf("ManifestDigest = %q, want empty", plan.ManifestDigest)
	}
	if got := len(plan.Manifest.Chunks); got != 0 {
		t.Fatalf("chunks = %d, want 0 without manifest", got)
	}
}

func TestBuildPullPlanDryRunFetchManifest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	diskData := []byte("bootable")
	manifest, blobs := pullCompressedTestManifest(t, diskData)
	configData := []byte("{}")
	manifest.Config = ociimage.Descriptor{
		MediaType: ociimage.MediaTypeImageConfig,
		Size:      int64(len(configData)),
		Digest:    pushTestDigest(configData),
	}
	blobs[manifest.Config.Digest] = configData
	manifestData, manifestDigest := pullTestManifestData(t, manifest)
	manifestOut := filepath.Join(t.TempDir(), "selected-manifest.json")
	var blobGets atomic.Int32
	var blobHeads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/me/dev-vm/manifests/v1":
			w.Header().Set("Docker-Content-Digest", manifestDigest)
			_, _ = w.Write(manifestData)
		default:
			const prefix = "/v2/me/dev-vm/blobs/"
			if !strings.HasPrefix(r.URL.Path, prefix) {
				t.Fatalf("path = %q", r.URL.Path)
			}
			digest := strings.TrimPrefix(r.URL.Path, prefix)
			data, ok := blobs[digest]
			if !ok {
				http.NotFound(w, r)
				return
			}
			if r.Method == http.MethodHead {
				blobHeads.Add(1)
				return
			}
			if r.Method != http.MethodGet {
				t.Fatalf("method = %s, want GET or HEAD", r.Method)
			}
			blobGets.Add(1)
			_, _ = w.Write(data)
		}
	}))
	defer srv.Close()

	plan, err := buildPullPlan("ghcr.io/me/dev-vm:v1", pullOptions{
		DryRun:          true,
		FetchManifest:   true,
		VerifyBlobs:     true,
		ManifestOut:     manifestOut,
		RegistryBaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("buildPullPlan(): %v", err)
	}
	if plan.ManifestDigest != manifestDigest {
		t.Fatalf("ManifestDigest = %q, want %q", plan.ManifestDigest, manifestDigest)
	}
	if plan.DigestRef != "ghcr.io/me/dev-vm@"+manifestDigest {
		t.Fatalf("DigestRef = %q, want selected digest ref", plan.DigestRef)
	}
	if string(plan.ManifestRaw) != string(manifestData) {
		t.Fatalf("ManifestRaw = %q, want exact registry bytes %q", string(plan.ManifestRaw), string(manifestData))
	}
	if err := writePullManifestOut(plan); err != nil {
		t.Fatalf("writePullManifestOut(): %v", err)
	}
	gotManifestOut, err := os.ReadFile(manifestOut)
	if err != nil {
		t.Fatalf("read manifest-out: %v", err)
	}
	if string(gotManifestOut) != string(manifestData) || digestData(gotManifestOut) != manifestDigest {
		t.Fatalf("manifest-out = %q digest=%s, want registry bytes digest %s", string(gotManifestOut), digestData(gotManifestOut), manifestDigest)
	}
	if got := len(plan.Manifest.Chunks); got != 1 {
		t.Fatalf("chunks = %d, want 1", got)
	}
	if plan.FetchDiskChunks != 1 || plan.FetchMetadataBlobs != 3 {
		t.Fatalf("fetch preflight = disk:%d metadata:%d, want disk 1 metadata 3", plan.FetchDiskChunks, plan.FetchMetadataBlobs)
	}
	if plan.BlobAudit != "ok" || plan.BlobDescriptors != 4 || len(plan.MissingBlobs) != 0 {
		t.Fatalf("blob audit = status:%q descriptors:%d missing:%v, want ok 4 descriptors", plan.BlobAudit, plan.BlobDescriptors, plan.MissingBlobs)
	}
	if got := blobHeads.Load(); got != 4 {
		t.Fatalf("blob HEADs = %d, want 4", got)
	}
	if got := blobGets.Load(); got != 0 {
		t.Fatalf("blob GETs = %d, want zero for manifest-only dry-run", got)
	}

	var out strings.Builder
	printPullDryRun(&out, plan)
	if !strings.Contains(out.String(), "blob audit: ok (4 descriptors") {
		t.Fatalf("dry-run output %q missing blob audit", out.String())
	}
	if !strings.Contains(out.String(), "manifest out: "+manifestOut) {
		t.Fatalf("dry-run output %q missing manifest out", out.String())
	}
	if !strings.Contains(out.String(), "digest ref: ghcr.io/me/dev-vm@"+manifestDigest) {
		t.Fatalf("dry-run output %q missing digest ref", out.String())
	}

	var jsonOut strings.Builder
	if err := printPullDryRunJSON(&jsonOut, plan); err != nil {
		t.Fatalf("printPullDryRunJSON(): %v", err)
	}
	var got pullDryRunOutput
	if err := json.Unmarshal([]byte(jsonOut.String()), &got); err != nil {
		t.Fatalf("Unmarshal(JSON): %v\n%s", err, jsonOut.String())
	}
	if got.BlobAudit == nil || got.BlobAudit.Status != "ok" || got.BlobAudit.Descriptors != 4 {
		t.Fatalf("JSON blob audit = %+v, want ok 4 descriptors", got.BlobAudit)
	}
	if got.ManifestOut != manifestOut {
		t.Fatalf("JSON manifest_out = %q, want %q", got.ManifestOut, manifestOut)
	}
	if got.DigestRef != "ghcr.io/me/dev-vm@"+manifestDigest {
		t.Fatalf("JSON digest_ref = %q, want selected digest ref", got.DigestRef)
	}
}

func TestBuildPullPlanDryRunFetchManifestPlatform(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
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
	manifestOut := filepath.Join(t.TempDir(), "linux-manifest.json")
	indexOut := filepath.Join(t.TempDir(), "index.json")
	indexData := remoteInspectIndexData(t, index)

	plan, err := buildPullPlan("ghcr.io/me/dev-vm:v1", pullOptions{
		DryRun:          true,
		FetchManifest:   true,
		ManifestOut:     manifestOut,
		IndexOut:        indexOut,
		Platform:        "linux/arm64",
		RegistryBaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("buildPullPlan(): %v", err)
	}
	if plan.ManifestDigest != linuxDigest || plan.ManifestResolution.SelectedDigest != linuxDigest {
		t.Fatalf("digest = manifest:%q selected:%q, want linux %q", plan.ManifestDigest, plan.ManifestResolution.SelectedDigest, linuxDigest)
	}
	if plan.DigestRef != "ghcr.io/me/dev-vm@"+linuxDigest || plan.IndexDigestRef != "ghcr.io/me/dev-vm@sha256:index" {
		t.Fatalf("digest refs = selected:%q index:%q, want linux child and index refs", plan.DigestRef, plan.IndexDigestRef)
	}
	if string(plan.ManifestRaw) != string(linuxData) {
		t.Fatalf("ManifestRaw = %q, want exact selected child bytes %q", string(plan.ManifestRaw), string(linuxData))
	}
	if string(plan.ManifestResolution.IndexData) != string(indexData) {
		t.Fatalf("IndexData = %q, want exact registry index bytes %q", string(plan.ManifestResolution.IndexData), string(indexData))
	}
	if err := writePullManifestOut(plan); err != nil {
		t.Fatalf("writePullManifestOut(): %v", err)
	}
	if err := writePullIndexOut(plan); err != nil {
		t.Fatalf("writePullIndexOut(): %v", err)
	}
	gotManifestOut, err := os.ReadFile(manifestOut)
	if err != nil {
		t.Fatalf("read manifest-out: %v", err)
	}
	if string(gotManifestOut) != string(linuxData) || digestData(gotManifestOut) != linuxDigest {
		t.Fatalf("manifest-out = %q digest=%s, want selected child digest %s", string(gotManifestOut), digestData(gotManifestOut), linuxDigest)
	}
	gotIndexOut, err := os.ReadFile(indexOut)
	if err != nil {
		t.Fatalf("read index-out: %v", err)
	}
	if string(gotIndexOut) != string(indexData) || plan.ManifestResolution.IndexDigest != "sha256:index" {
		t.Fatalf("index-out = %q recorded_digest=%s, want registry index bytes recorded as sha256:index", string(gotIndexOut), plan.ManifestResolution.IndexDigest)
	}
	if plan.ManifestResolution.IndexDigest != "sha256:index" || remotePlatformString(plan.ManifestResolution.SelectedPlatform) != "linux/arm64" {
		t.Fatalf("resolution = %+v, want linux index selection", plan.ManifestResolution)
	}
	if plan.Manifest.Annotations.UncompressedDiskSize != int64(len("linux-child")) {
		t.Fatalf("disk size = %d, want linux child", plan.Manifest.Annotations.UncompressedDiskSize)
	}

	var out strings.Builder
	printPullDryRun(&out, plan)
	for _, want := range []string{"index digest: sha256:index", "index out: " + indexOut, "selected digest: " + linuxDigest, "platform: linux/arm64", "index manifests: 2", "platform=darwin/arm64", "* " + linuxDigest} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("dry-run output %q missing %q", out.String(), want)
		}
	}

	var jsonOut strings.Builder
	if err := printPullDryRunJSON(&jsonOut, plan); err != nil {
		t.Fatalf("printPullDryRunJSON(): %v", err)
	}
	var got pullDryRunOutput
	if err := json.Unmarshal([]byte(jsonOut.String()), &got); err != nil {
		t.Fatalf("Unmarshal(JSON): %v\n%s", err, jsonOut.String())
	}
	if !got.ResolvedFromIndex || got.SelectedPlatform != "linux/arm64" || got.SelectedDigest != linuxDigest {
		t.Fatalf("JSON resolution = %+v, want linux index selection", got)
	}
	if got.IndexOut != indexOut || got.IndexDigestRef != "ghcr.io/me/dev-vm@sha256:index" {
		t.Fatalf("JSON index fields = out:%q ref:%q, want %q and index digest ref", got.IndexOut, got.IndexDigestRef, indexOut)
	}
	if len(got.IndexManifests) != 2 || got.IndexManifests[0].Platform != "darwin/arm64" || got.IndexManifests[1].Platform != "linux/arm64" || !got.IndexManifests[1].Selected {
		t.Fatalf("JSON index manifests = %+v, want selected linux candidate", got.IndexManifests)
	}
}

func TestBuildPullPlanDryRunFetchManifestAllPlatforms(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	darwinBase, _, _ := pullCompressedChunkedTestManifest(t, []byte("darwin"), 3)
	setRemoteInspectTestConfig(&darwinBase, []byte("darwin-base-config"))
	darwinBaseData, darwinBaseDigest := pullTestManifestData(t, darwinBase)
	darwinManifest := darwinBase
	darwinManifest.Annotations = cloneStringMap(darwinManifest.Annotations)
	darwinManifest.Annotations[ociimage.CoveBaseManifest] = darwinBaseDigest
	linuxManifest, _, _ := pullCompressedChunkedTestManifest(t, []byte("linux-child"), 5)
	setRemoteInspectTestConfig(&linuxManifest, []byte("linux-config"))
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
	blobs := remoteInspectExistingBlobs(darwinManifest, darwinBase, linuxManifest)
	srv := newRemoteInspectMultiIndexBlobRegistry(t, index, map[string][]byte{
		darwinDigest:     darwinData,
		darwinBaseDigest: darwinBaseData,
		linuxDigest:      linuxData,
	}, blobs)
	t.Cleanup(srv.Close)
	manifestDir := filepath.Join(t.TempDir(), "bundle")
	indexData := remoteInspectIndexData(t, index)

	plan, err := buildPullPlan("ghcr.io/me/dev-vm:v1", pullOptions{
		DryRun:          true,
		FetchManifest:   true,
		VerifyBlobs:     true,
		AllPlatforms:    true,
		ManifestDir:     manifestDir,
		Platform:        "linux/arm64",
		RegistryBaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("buildPullPlan(): %v", err)
	}
	if plan.ManifestDigest != linuxDigest || plan.ManifestResolution.SelectedDigest != linuxDigest {
		t.Fatalf("digest = manifest:%q selected:%q, want linux %q", plan.ManifestDigest, plan.ManifestResolution.SelectedDigest, linuxDigest)
	}
	if len(plan.IndexManifests) != 2 {
		t.Fatalf("index manifests = %d, want 2", len(plan.IndexManifests))
	}
	children := map[string]ImageRemoteIndexManifest{}
	for _, child := range plan.IndexManifests {
		children[child.Platform] = child
	}
	if darwin := children["darwin/arm64"]; darwin.Format != "cove" || darwin.BaseChainAudit != "ok" || darwin.BlobAudit != "ok" || darwin.BaseChainDepth != 1 {
		t.Fatalf("darwin child = %+v, want cove ok base/blob audits", darwin)
	}
	if linux := children["linux/arm64"]; linux.Format != "cove" || linux.BaseChainAudit != "missing" || linux.BlobAudit != "ok" || !linux.Selected {
		t.Fatalf("linux child = %+v, want selected cove missing-base ok-blob audit", linux)
	}
	if got := srv.blobGets.Load(); got != 0 {
		t.Fatalf("blob GETs = %d, want 0", got)
	}
	if err := writePullManifestDir(plan); err != nil {
		t.Fatalf("writePullManifestDir: %v", err)
	}
	assertManifestBundleFile(t, filepath.Join(manifestDir, "index.json"), indexData)
	assertManifestBundleFile(t, filepath.Join(manifestDir, "selected.json"), linuxData)
	assertManifestBundleFile(t, filepath.Join(manifestDir, "manifests", manifestBundleDigestName(darwinDigest)+".json"), darwinData)
	assertManifestBundleFile(t, filepath.Join(manifestDir, "manifests", manifestBundleDigestName(linuxDigest)+".json"), linuxData)
	summary := readManifestBundleSummary(t, manifestDir)
	if summary.Source != "pull dry-run" || summary.Ref != "ghcr.io/me/dev-vm:v1" || summary.VM != "dev-vm" || summary.Target == "" {
		t.Fatalf("bundle summary pull header = %+v, want pull dry-run ref/vm/target", summary)
	}
	if summary.IndexFileDigest != digestData(indexData) || summary.SelectedFileDigest != linuxDigest || summary.SelectedPlatform != "linux/arm64" {
		t.Fatalf("bundle summary pull digests = index:%q selected:%q platform:%q, want exact files and platform", summary.IndexFileDigest, summary.SelectedFileDigest, summary.SelectedPlatform)
	}
	if summary.Format != "cove" || summary.DiskSize != int64(len("linux-child")) || summary.DiskFormat != "raw" || summary.ChildCount != 2 {
		t.Fatalf("bundle summary pull format/disk/children = %s/%d/%s/%d, want cove linux raw two children", summary.Format, summary.DiskSize, summary.DiskFormat, summary.ChildCount)
	}
	if summary.BlobAudit == nil || summary.BlobAudit.Status != "ok" || summary.BlobAudit.Descriptors == 0 {
		t.Fatalf("bundle summary blob audit = %+v, want selected ok blob audit", summary.BlobAudit)
	}
	if got := summary.Children[0]; got.Digest != darwinDigest || got.BaseChainAudit != "ok" || got.BlobAudit == nil || got.BlobAudit.Status != "ok" {
		t.Fatalf("bundle summary darwin child = %+v, want base/blob audit", got)
	}
	if got := summary.Children[1]; got.Digest != linuxDigest || got.Path != manifestBundleChildPath(linuxDigest) || !got.Selected || got.BaseChainAudit != "missing" || got.BlobAudit == nil || got.BlobAudit.Status != "ok" {
		t.Fatalf("bundle summary linux child = %+v, want selected missing-base ok-blob audit", got)
	}

	var out strings.Builder
	printPullDryRun(&out, plan)
	for _, want := range []string{"manifest dir: " + manifestDir, "index manifests: 2", "format=cove", "base_audit=ok", "base_audit=missing", "blob_audit=ok", "disk_size=11 B"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("dry-run output %q missing %q", out.String(), want)
		}
	}

	var jsonOut strings.Builder
	if err := printPullDryRunJSON(&jsonOut, plan); err != nil {
		t.Fatalf("printPullDryRunJSON(): %v", err)
	}
	var got pullDryRunOutput
	if err := json.Unmarshal([]byte(jsonOut.String()), &got); err != nil {
		t.Fatalf("Unmarshal(JSON): %v\n%s", err, jsonOut.String())
	}
	if len(got.IndexManifests) != 2 || got.IndexManifests[0].Format != "cove" || got.IndexManifests[0].BaseChainAudit != "ok" || got.IndexManifests[1].BlobAudit != "ok" {
		t.Fatalf("JSON index manifests = %+v, want detailed child audits", got.IndexManifests)
	}
	if got.ManifestDir != manifestDir {
		t.Fatalf("JSON manifest_dir = %q, want %q", got.ManifestDir, manifestDir)
	}
}

func TestRecordPullDryRunImportBlobs(t *testing.T) {
	tests := []struct {
		name string
		plan *pullPlan
		want []string
	}{
		{
			name: "lume",
			plan: &pullPlan{
				Manifest: ociimage.ParsedManifest{
					Format: ociimage.FormatLume,
					Lume: ociimage.LumeManifest{
						DiskParts: []ociimage.LumeLayer{
							{Descriptor: ociimage.Descriptor{Digest: "sha256:part-a", Size: 1}, PartNumber: 1, Title: "disk.img.part.aa"},
							{Descriptor: ociimage.Descriptor{Digest: "sha256:part-b", Size: 2}, PartNumber: 2},
						},
						NvramLayer:  &ociimage.Descriptor{Digest: "sha256:nvram", Size: 3},
						ConfigLayer: &ociimage.Descriptor{Digest: "sha256:config", Size: 4},
					},
				},
			},
			want: []string{"disk.img.part.aa", "disk-part[2]", "nvram.bin", "config.json"},
		},
		{
			name: "tart",
			plan: &pullPlan{
				Manifest: ociimage.ParsedManifest{
					Format: ociimage.FormatTart,
					Tart: ociimage.TartManifest{
						NVRAMLayer:  ociimage.Descriptor{Digest: "sha256:nvram", Size: 1},
						ConfigLayer: ociimage.Descriptor{Digest: "sha256:config", Size: 2},
						DiskLayers: []ociimage.TartDiskLayer{
							{Descriptor: ociimage.Descriptor{Digest: "sha256:disk-a", Size: 3}},
							{Descriptor: ociimage.Descriptor{Digest: "sha256:disk-b", Size: 4}},
						},
					},
				},
			},
			want: []string{"nvram", "config", "disk[0]", "disk[1]"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recordPullDryRunImportBlobs(tt.plan)
			if got := len(tt.plan.FetchBlobDescriptors); got != len(tt.want) {
				t.Fatalf("FetchBlobDescriptors = %d, want %d", got, len(tt.want))
			}
			for i, want := range tt.want {
				if got := tt.plan.FetchBlobDescriptors[i].Name; got != want {
					t.Fatalf("FetchBlobDescriptors[%d].Name = %q, want %q", i, got, want)
				}
			}
		})
	}
}

func TestPullDiskDownloadsRegistryChunks(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	diskData := []byte("bootable")
	manifest, blobs := pullCompressedTestManifest(t, diskData)
	srv := pullTestRegistry(t, manifest, blobs)
	defer srv.Close()

	opts := pullOptions{RegistryBaseURL: srv.URL}
	plan, err := buildPullPlan("ghcr.io/me/dev-vm:v1", opts)
	if err != nil {
		t.Fatalf("buildPullPlan(): %v", err)
	}
	manifestDigest := plan.ManifestDigest
	if err := pullDisk(context.Background(), plan, opts); err != nil {
		t.Fatalf("pullDisk(): %v", err)
	}

	vmDir := filepath.Join(home, ".vz", "vms", "dev-vm")
	got, err := os.ReadFile(filepath.Join(vmDir, "disk.img"))
	if err != nil {
		t.Fatalf("ReadFile(disk.img): %v", err)
	}
	if !bytes.Equal(got, diskData) {
		t.Fatalf("disk = %v, want %v", got, diskData)
	}
	if _, err := os.Stat(filepath.Join(vmDir, "disk.img.partial")); !os.IsNotExist(err) {
		t.Fatalf("partial stat error = %v, want not exist", err)
	}
	provenance, err := os.ReadFile(filepath.Join(vmDir, "disk.provenance"))
	if err != nil {
		t.Fatalf("ReadFile(disk.provenance): %v", err)
	}
	if string(provenance) != manifestDigest+"\n" {
		t.Fatalf("provenance = %q, want %s", string(provenance), manifestDigest)
	}
	for _, tt := range []struct {
		name string
		want string
	}{
		{name: "aux.img", want: "aux"},
		{name: "hw.model", want: "hw"},
		{name: "machine.id", want: "machine"},
	} {
		got, err := os.ReadFile(filepath.Join(vmDir, tt.name))
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", tt.name, err)
		}
		if string(got) != tt.want {
			t.Fatalf("%s = %q, want %q", tt.name, string(got), tt.want)
		}
	}
}

func TestPullDiskReusesStoredBlobs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	diskData := []byte("bootable")
	manifest, blobs := pullCompressedTestManifest(t, diskData)
	manifestData, manifestDigest := pullTestManifestData(t, manifest)
	var blobGets atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/me/dev-vm/manifests/v1":
			w.Header().Set("Docker-Content-Digest", manifestDigest)
			_, _ = w.Write(manifestData)
		default:
			const prefix = "/v2/me/dev-vm/blobs/"
			if !strings.HasPrefix(r.URL.Path, prefix) {
				t.Fatalf("path = %q", r.URL.Path)
			}
			blobGets.Add(1)
			digest := strings.TrimPrefix(r.URL.Path, prefix)
			data, ok := blobs[digest]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(data)
		}
	}))
	defer srv.Close()

	opts := pullOptions{RegistryBaseURL: srv.URL, As: "first"}
	plan, err := buildPullPlan("ghcr.io/me/dev-vm:v1", opts)
	if err != nil {
		t.Fatalf("buildPullPlan(first): %v", err)
	}
	if err := pullDisk(context.Background(), plan, opts); err != nil {
		t.Fatalf("pullDisk(first): %v", err)
	}
	firstBlobGets := blobGets.Load()
	if firstBlobGets == 0 {
		t.Fatal("first pull did not fetch any blobs")
	}

	opts.As = "second"
	plan, err = buildPullPlan("ghcr.io/me/dev-vm:v1", opts)
	if err != nil {
		t.Fatalf("buildPullPlan(second): %v", err)
	}
	if err := pullDisk(context.Background(), plan, opts); err != nil {
		t.Fatalf("pullDisk(second): %v", err)
	}
	if got := blobGets.Load(); got != firstBlobGets {
		t.Fatalf("blob GETs after second pull = %d, want %d", got, firstBlobGets)
	}
}

func TestPullDiskReusesBaseDiskClone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if !SupportsClonefile(home) {
		t.Skip("clonefile not supported")
	}

	baseDisk := []byte("aaaabbbb")
	targetDisk := []byte("aaaacccc")
	baseManifest, _, _ := pullCompressedChunkedTestManifest(t, baseDisk, 4)
	baseData, baseDigest := pullTestManifestData(t, baseManifest)
	blobStore := store.New("")
	if err := blobStore.StoreManifest(baseDigest, baseData); err != nil {
		t.Fatalf("StoreManifest(base): %v", err)
	}
	writePullBaseVM(t, home, "base", baseDigest, baseDisk)

	manifest, blobs, diskDigests := pullCompressedChunkedTestManifest(t, targetDisk, 4)
	manifest.Annotations[ociimage.CoveBaseManifest] = baseDigest
	var diskGets atomic.Int32
	srv := pullCountingRegistry(t, manifest, blobs, diskDigests, &diskGets)
	defer srv.Close()

	opts := pullOptions{RegistryBaseURL: srv.URL, As: "child"}
	plan, err := buildPullPlan("ghcr.io/me/dev-vm:v1", opts)
	if err != nil {
		t.Fatalf("buildPullPlan(): %v", err)
	}
	if err := pullDisk(context.Background(), plan, opts); err != nil {
		t.Fatalf("pullDisk(): %v", err)
	}
	got, err := os.ReadFile(filepath.Join(home, ".vz", "vms", "child", "disk.img"))
	if err != nil {
		t.Fatalf("ReadFile(disk.img): %v", err)
	}
	if !bytes.Equal(got, targetDisk) {
		t.Fatalf("disk = %q, want %q", got, targetDisk)
	}
	if got := diskGets.Load(); got != 1 {
		t.Fatalf("disk blob GETs = %d, want one changed chunk", got)
	}
	if plan.BaseReusePath == "" || plan.BaseReuseDiskFormat != "raw" || plan.BaseReuseChunks != 1 || plan.BaseReuseBytes != 4 {
		t.Fatalf("base reuse summary = path:%q format:%q chunks:%d bytes:%d, want raw one 4-byte chunk", plan.BaseReusePath, plan.BaseReuseDiskFormat, plan.BaseReuseChunks, plan.BaseReuseBytes)
	}
}

func TestPlanPullBaseReuseRequiresManifestDiskFormatMatch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	baseDisk := []byte("aaaabbbb")
	targetDisk := []byte("aaaacccc")
	baseManifest, _, _ := pullCompressedChunkedTestManifest(t, baseDisk, 4)
	baseData, baseDigest := pullTestManifestData(t, baseManifest)
	blobStore := store.New("")
	if err := blobStore.StoreManifest(baseDigest, baseData); err != nil {
		t.Fatalf("StoreManifest(base): %v", err)
	}
	writePullBaseVM(t, home, "base", baseDigest, baseDisk)

	manifest, _, _ := pullCompressedChunkedTestManifest(t, targetDisk, 4)
	manifest.Annotations[ociimage.CoveBaseManifest] = baseDigest
	manifest.Annotations[ociimage.CoveDiskFormat] = "asif"
	parsed, err := ociimage.ParseManifest(manifest)
	if err != nil {
		t.Fatalf("ParseManifest(target): %v", err)
	}

	reuse, err := planPullBaseReuse(&pullPlan{
		VMDir:    filepath.Join(home, ".vz", "vms", "child"),
		Manifest: parsed,
	}, blobStore)
	if err != nil {
		t.Fatalf("planPullBaseReuse(): %v", err)
	}
	if reuse != nil {
		t.Fatalf("base reuse = %+v, want nil for raw/asif mismatch", reuse)
	}
}

func TestPlanPullBaseReuseRequiresBaseDiskFileFormat(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	oldInfo := retrieveDiskImageInfo
	t.Cleanup(func() { retrieveDiskImageInfo = oldInfo })

	baseDisk := []byte("aaaabbbb")
	targetDisk := []byte("aaaacccc")
	baseManifest, _, _ := pullCompressedChunkedTestManifest(t, baseDisk, 4)
	baseManifest.Annotations[ociimage.CoveDiskFormat] = "asif"
	baseData, baseDigest := pullTestManifestData(t, baseManifest)
	blobStore := store.New("")
	if err := blobStore.StoreManifest(baseDigest, baseData); err != nil {
		t.Fatalf("StoreManifest(base): %v", err)
	}
	writePullBaseVM(t, home, "base", baseDigest, baseDisk)

	manifest, _, _ := pullCompressedChunkedTestManifest(t, targetDisk, 4)
	manifest.Annotations[ociimage.CoveBaseManifest] = baseDigest
	manifest.Annotations[ociimage.CoveDiskFormat] = "asif"
	parsed, err := ociimage.ParseManifest(manifest)
	if err != nil {
		t.Fatalf("ParseManifest(target): %v", err)
	}
	plan := &pullPlan{
		VMDir:    filepath.Join(home, ".vz", "vms", "child"),
		Manifest: parsed,
	}

	retrieveDiskImageInfo = func(string) (*diskimages2.ImageInfo, error) {
		return &diskimages2.ImageInfo{Raw: map[string]string{"Image Format": "raw"}}, nil
	}
	reuse, err := planPullBaseReuse(plan, blobStore)
	if err != nil {
		t.Fatalf("planPullBaseReuse(raw): %v", err)
	}
	if reuse != nil {
		t.Fatalf("base reuse with raw disk = %+v, want nil", reuse)
	}

	retrieveDiskImageInfo = func(string) (*diskimages2.ImageInfo, error) {
		return &diskimages2.ImageInfo{Raw: map[string]string{"Image Format": "ASIF"}}, nil
	}
	reuse, err = planPullBaseReuse(plan, blobStore)
	if err != nil {
		t.Fatalf("planPullBaseReuse(asif): %v", err)
	}
	if reuse == nil {
		t.Fatal("base reuse = nil, want ASIF base reuse")
	}
}

func TestPullDiskReusesRegistryBaseCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if !SupportsClonefile(home) {
		t.Skip("clonefile not supported")
	}

	baseDisk := []byte("aaaabbbb")
	targetDisk := []byte("aaaacccc")
	baseManifest, _, _ := pullCompressedChunkedTestManifest(t, baseDisk, 4)
	baseData, baseDigest := pullTestManifestData(t, baseManifest)
	blobStore := store.New("")
	if err := blobStore.StoreManifest(baseDigest, baseData); err != nil {
		t.Fatalf("StoreManifest(base): %v", err)
	}
	cacheDir, err := buildRegistryBaseCacheDir(buildOptions{}, baseDigest)
	if err != nil {
		t.Fatalf("buildRegistryBaseCacheDir(): %v", err)
	}
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		t.Fatalf("MkdirAll(cacheDir): %v", err)
	}
	for name, data := range map[string][]byte{
		"disk.img":        baseDisk,
		"aux.img":         []byte("aux"),
		"disk.provenance": []byte(baseDigest + "\n"),
	} {
		if err := os.WriteFile(filepath.Join(cacheDir, name), data, 0644); err != nil {
			t.Fatalf("WriteFile(%s): %v", name, err)
		}
	}

	manifest, blobs, diskDigests := pullCompressedChunkedTestManifest(t, targetDisk, 4)
	manifest.Annotations[ociimage.CoveBaseManifest] = baseDigest
	var diskGets atomic.Int32
	srv := pullCountingRegistry(t, manifest, blobs, diskDigests, &diskGets)
	defer srv.Close()

	opts := pullOptions{RegistryBaseURL: srv.URL, As: "child"}
	plan, err := buildPullPlan("ghcr.io/me/dev-vm:v1", opts)
	if err != nil {
		t.Fatalf("buildPullPlan(): %v", err)
	}
	if err := pullDisk(context.Background(), plan, opts); err != nil {
		t.Fatalf("pullDisk(): %v", err)
	}
	got, err := os.ReadFile(filepath.Join(home, ".vz", "vms", "child", "disk.img"))
	if err != nil {
		t.Fatalf("ReadFile(disk.img): %v", err)
	}
	if !bytes.Equal(got, targetDisk) {
		t.Fatalf("disk = %q, want %q", got, targetDisk)
	}
	if got := diskGets.Load(); got != 1 {
		t.Fatalf("disk blob GETs = %d, want one changed chunk", got)
	}
}

func TestPullDiskZerosChangedBaseChunk(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if !SupportsClonefile(home) {
		t.Skip("clonefile not supported")
	}

	baseDisk := []byte("aaaa")
	targetDisk := []byte{0, 0, 0, 0}
	baseManifest, _, _ := pullCompressedChunkedTestManifest(t, baseDisk, 4)
	baseData, baseDigest := pullTestManifestData(t, baseManifest)
	blobStore := store.New("")
	if err := blobStore.StoreManifest(baseDigest, baseData); err != nil {
		t.Fatalf("StoreManifest(base): %v", err)
	}
	writePullBaseVM(t, home, "base", baseDigest, baseDisk)

	manifest, blobs, diskDigests := pullCompressedChunkedTestManifest(t, targetDisk, 4)
	manifest.Annotations[ociimage.CoveBaseManifest] = baseDigest
	var diskGets atomic.Int32
	srv := pullCountingRegistry(t, manifest, blobs, diskDigests, &diskGets)
	defer srv.Close()

	opts := pullOptions{RegistryBaseURL: srv.URL, As: "child"}
	plan, err := buildPullPlan("ghcr.io/me/dev-vm:v1", opts)
	if err != nil {
		t.Fatalf("buildPullPlan(): %v", err)
	}
	if err := pullDisk(context.Background(), plan, opts); err != nil {
		t.Fatalf("pullDisk(): %v", err)
	}
	got, err := os.ReadFile(filepath.Join(home, ".vz", "vms", "child", "disk.img"))
	if err != nil {
		t.Fatalf("ReadFile(disk.img): %v", err)
	}
	if !bytes.Equal(got, targetDisk) {
		t.Fatalf("disk = %v, want zeros", got)
	}
	if got := diskGets.Load(); got != 0 {
		t.Fatalf("disk blob GETs = %d, want zero for sparse target chunk", got)
	}
}

func TestPullDiskDownloadsChunksConcurrently(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	diskData := []byte("aaaabbbbccccdddd")
	manifest, blobs, diskDigests := pullCompressedChunkedTestManifest(t, diskData, 4)
	manifestData, manifestDigest := pullTestManifestData(t, manifest)

	var (
		mu         sync.Mutex
		active     int
		concurrent bool
	)
	release := make(chan struct{})
	var releaseOnce sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/me/dev-vm/manifests/v1":
			w.Header().Set("Docker-Content-Digest", manifestDigest)
			_, _ = w.Write(manifestData)
		default:
			const prefix = "/v2/me/dev-vm/blobs/"
			if !strings.HasPrefix(r.URL.Path, prefix) {
				t.Fatalf("path = %q", r.URL.Path)
			}
			digest := strings.TrimPrefix(r.URL.Path, prefix)
			data, ok := blobs[digest]
			if !ok {
				http.NotFound(w, r)
				return
			}
			if diskDigests[digest] {
				mu.Lock()
				active++
				if active > 1 {
					concurrent = true
					releaseOnce.Do(func() { close(release) })
				}
				mu.Unlock()
				select {
				case <-release:
				case <-time.After(250 * time.Millisecond):
					releaseOnce.Do(func() { close(release) })
				}
				mu.Lock()
				active--
				mu.Unlock()
			}
			_, _ = w.Write(data)
		}
	}))
	defer srv.Close()

	opts := pullOptions{RegistryBaseURL: srv.URL}
	plan, err := buildPullPlan("ghcr.io/me/dev-vm:v1", opts)
	if err != nil {
		t.Fatalf("buildPullPlan(): %v", err)
	}
	if err := pullDisk(context.Background(), plan, opts); err != nil {
		t.Fatalf("pullDisk(): %v", err)
	}
	mu.Lock()
	gotConcurrent := concurrent
	mu.Unlock()
	if !gotConcurrent {
		t.Fatal("disk chunks were not fetched concurrently")
	}
	got, err := os.ReadFile(filepath.Join(home, ".vz", "vms", "dev-vm", "disk.img"))
	if err != nil {
		t.Fatalf("ReadFile(disk.img): %v", err)
	}
	if !bytes.Equal(got, diskData) {
		t.Fatalf("disk = %q, want %q", got, diskData)
	}
}

func TestPullDiskLeavesPartialOnBlobFailure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	manifest, blobs := pullCompressedTestManifest(t, []byte("bootable"))
	for digest := range blobs {
		blobs[digest] = []byte("corrupt")
	}
	srv := pullTestRegistry(t, manifest, blobs)
	defer srv.Close()

	opts := pullOptions{RegistryBaseURL: srv.URL}
	plan, err := buildPullPlan("ghcr.io/me/dev-vm:v1", opts)
	if err != nil {
		t.Fatalf("buildPullPlan(): %v", err)
	}
	err = pullDisk(context.Background(), plan, opts)
	if err == nil {
		t.Fatal("pullDisk() error = nil, want blob failure")
	}

	vmDir := filepath.Join(home, ".vz", "vms", "dev-vm")
	if _, err := os.Stat(filepath.Join(vmDir, "disk.img.partial")); err != nil {
		t.Fatalf("partial stat error = %v, want partial disk", err)
	}
	if _, err := os.Stat(filepath.Join(vmDir, "disk.img")); !os.IsNotExist(err) {
		t.Fatalf("disk stat error = %v, want not exist", err)
	}
	if _, err := os.Stat(filepath.Join(vmDir, "disk.provenance")); !os.IsNotExist(err) {
		t.Fatalf("provenance stat error = %v, want not exist", err)
	}
}

func TestPullDiskResumeRewritesPartial(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	diskData := []byte("bootable")
	manifest, blobs := pullCompressedTestManifest(t, diskData)
	srv := pullTestRegistry(t, manifest, blobs)
	defer srv.Close()

	vmDir := filepath.Join(home, ".vz", "vms", "dev-vm")
	if err := os.MkdirAll(vmDir, 0755); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	partialPath := filepath.Join(vmDir, "disk.img.partial")
	if err := os.WriteFile(partialPath, []byte("stale partial bytes"), 0600); err != nil {
		t.Fatalf("WriteFile(partial): %v", err)
	}

	opts := pullOptions{RegistryBaseURL: srv.URL, Resume: true}
	plan, err := buildPullPlan("ghcr.io/me/dev-vm:v1", opts)
	if err != nil {
		t.Fatalf("buildPullPlan(resume): %v", err)
	}
	if err := pullDisk(context.Background(), plan, opts); err != nil {
		t.Fatalf("pullDisk(resume): %v", err)
	}
	got, err := os.ReadFile(filepath.Join(vmDir, "disk.img"))
	if err != nil {
		t.Fatalf("ReadFile(disk.img): %v", err)
	}
	if !bytes.Equal(got, diskData) {
		t.Fatalf("disk = %q, want %q", got, diskData)
	}
	if _, err := os.Stat(partialPath); !os.IsNotExist(err) {
		t.Fatalf("partial stat error = %v, want not exist", err)
	}
}

func TestPullDiskResumeZerosPartialSparseChunk(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	diskData := []byte{0, 0, 0, 0}
	manifest, blobs, diskDigests := pullCompressedChunkedTestManifest(t, diskData, 4)
	var diskGets atomic.Int32
	srv := pullCountingRegistry(t, manifest, blobs, diskDigests, &diskGets)
	defer srv.Close()

	vmDir := filepath.Join(home, ".vz", "vms", "dev-vm")
	if err := os.MkdirAll(vmDir, 0755); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	if err := os.WriteFile(filepath.Join(vmDir, "disk.img.partial"), []byte("xxxx"), 0600); err != nil {
		t.Fatalf("WriteFile(partial): %v", err)
	}

	opts := pullOptions{RegistryBaseURL: srv.URL, Resume: true}
	plan, err := buildPullPlan("ghcr.io/me/dev-vm:v1", opts)
	if err != nil {
		t.Fatalf("buildPullPlan(resume): %v", err)
	}
	if err := pullDisk(context.Background(), plan, opts); err != nil {
		t.Fatalf("pullDisk(resume): %v", err)
	}
	got, err := os.ReadFile(filepath.Join(vmDir, "disk.img"))
	if err != nil {
		t.Fatalf("ReadFile(disk.img): %v", err)
	}
	if !bytes.Equal(got, diskData) {
		t.Fatalf("disk = %v, want zeros", got)
	}
	if got := diskGets.Load(); got != 0 {
		t.Fatalf("disk blob GETs = %d, want zero for sparse chunk", got)
	}
}

func TestHandlePullDryRunOutput(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	manifestPath := writePullTestManifest(t)

	var out strings.Builder
	env := commandTestEnv()
	env.Stdout = &out
	if err := handlePull(env, []string{
		"--dry-run",
		"--manifest", manifestPath,
		"--as", "local-dev",
		"ghcr.io/me/dev-vm:v1",
	}); err != nil {
		t.Fatalf("handlePull(): %v", err)
	}
	for _, want := range []string{
		"Pull dry run",
		"ref: ghcr.io/me/dev-vm:v1",
		"vm: local-dev",
		"chunks: 1",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output %q missing %q", out.String(), want)
		}
	}
}

func TestHandlePullDryRunJSONOutput(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	manifestPath := writePullTestManifest(t)

	var out strings.Builder
	env := commandTestEnv()
	env.Stdout = &out
	if err := handlePull(env, []string{
		"--dry-run",
		"--json",
		"--manifest", manifestPath,
		"--as", "local-dev",
		"ghcr.io/me/dev-vm:v1",
	}); err != nil {
		t.Fatalf("handlePull(): %v", err)
	}
	var got pullDryRunOutput
	if err := json.Unmarshal([]byte(out.String()), &got); err != nil {
		t.Fatalf("Unmarshal(output): %v\n%s", err, out.String())
	}
	if got.Ref != "ghcr.io/me/dev-vm:v1" || got.VM != "local-dev" || got.Format != "cove" || !got.ManifestProvided || got.Chunks != 1 {
		t.Fatalf("JSON output = %+v, want cove dry-run plan", got)
	}
}

func TestHandlePullJSONRequiresDryRun(t *testing.T) {
	err := handlePull(commandTestEnv(), []string{"--json", "ghcr.io/me/dev-vm:v1"})
	if err == nil || !strings.Contains(err.Error(), "--json requires --dry-run") {
		t.Fatalf("handlePull() error = %v, want --json requires --dry-run", err)
	}
}

func TestHandlePullFetchManifestRequiresDryRun(t *testing.T) {
	err := handlePull(commandTestEnv(), []string{"--fetch-manifest", "ghcr.io/me/dev-vm:v1"})
	if err == nil || !strings.Contains(err.Error(), "--fetch-manifest requires --dry-run") {
		t.Fatalf("handlePull() error = %v, want --fetch-manifest requires --dry-run", err)
	}
}

func TestHandlePullVerifyBlobsRequiresDryRun(t *testing.T) {
	err := handlePull(commandTestEnv(), []string{"--verify-blobs", "ghcr.io/me/dev-vm:v1"})
	if err == nil || !strings.Contains(err.Error(), "--verify-blobs requires --dry-run") {
		t.Fatalf("handlePull() error = %v, want --verify-blobs requires --dry-run", err)
	}
}

func TestHandlePullVerifyBlobsRequiresManifest(t *testing.T) {
	err := handlePull(commandTestEnv(), []string{"--dry-run", "--verify-blobs", "ghcr.io/me/dev-vm:v1"})
	if err == nil || !strings.Contains(err.Error(), "--verify-blobs requires --fetch-manifest or --manifest") {
		t.Fatalf("handlePull() error = %v, want --verify-blobs requires manifest", err)
	}
}

func TestHandlePullAllPlatformsRequiresFetchManifest(t *testing.T) {
	err := handlePull(commandTestEnv(), []string{"--dry-run", "--all-platforms", "ghcr.io/me/dev-vm:v1"})
	if err == nil || !strings.Contains(err.Error(), "--all-platforms requires --fetch-manifest") {
		t.Fatalf("handlePull() error = %v, want --all-platforms requires fetch-manifest", err)
	}
}

func TestHandlePullManifestOutRequiresFetchManifest(t *testing.T) {
	err := handlePull(commandTestEnv(), []string{"--dry-run", "--manifest-out", "manifest.json", "ghcr.io/me/dev-vm:v1"})
	if err == nil || !strings.Contains(err.Error(), "--manifest-out requires --fetch-manifest") {
		t.Fatalf("handlePull() error = %v, want --manifest-out requires fetch-manifest", err)
	}
}

func TestHandlePullIndexOutRequiresFetchManifest(t *testing.T) {
	err := handlePull(commandTestEnv(), []string{"--dry-run", "--index-out", "index.json", "ghcr.io/me/dev-vm:v1"})
	if err == nil || !strings.Contains(err.Error(), "--index-out requires --fetch-manifest") {
		t.Fatalf("handlePull() error = %v, want --index-out requires fetch-manifest", err)
	}
}

func TestHandlePullManifestDirRequiresAllPlatforms(t *testing.T) {
	err := handlePull(commandTestEnv(), []string{"--dry-run", "--fetch-manifest", "--manifest-dir", "bundle", "ghcr.io/me/dev-vm:v1"})
	if err == nil || !strings.Contains(err.Error(), "--manifest-dir requires --all-platforms") {
		t.Fatalf("handlePull() error = %v, want --manifest-dir requires all-platforms", err)
	}
}

func TestHandlePullRejectsFetchManifestWithManifest(t *testing.T) {
	err := handlePull(commandTestEnv(), []string{
		"--dry-run",
		"--fetch-manifest",
		"--manifest", "manifest.json",
		"ghcr.io/me/dev-vm:v1",
	})
	if err == nil || !strings.Contains(err.Error(), "--fetch-manifest cannot be used with --manifest") {
		t.Fatalf("handlePull() error = %v, want --fetch-manifest/--manifest conflict", err)
	}
}

func TestHandlePullRequiresRef(t *testing.T) {
	err := handlePull(commandTestEnv(), []string{"--dry-run"})
	if err == nil || !strings.Contains(err.Error(), "usage: cove pull") {
		t.Fatalf("handlePull() error = %v, want usage", err)
	}
}

func TestParsePullArgs(t *testing.T) {
	opts, pos, err := parsePullArgs([]string{
		"--as", "local-dev",
		"--resume",
		"--dry-run",
		"--json",
		"--manifest", "manifest.json",
		"ghcr.io/me/dev-vm:v1",
	}, ioDiscard{})
	if err != nil {
		t.Fatalf("parsePullArgs(): %v", err)
	}
	if !opts.DryRun || !opts.JSON || !opts.Resume || opts.As != "local-dev" || opts.ManifestPath != "manifest.json" {
		t.Fatalf("opts = %#v", opts)
	}
	if strings.Join(pos, ",") != "ghcr.io/me/dev-vm:v1" {
		t.Fatalf("pos = %#v", pos)
	}
}

func TestParsePullArgsHelpReturnsNoError(t *testing.T) {
	opts, pos, err := parsePullArgs([]string{"-h"}, ioDiscard{})
	if err != nil {
		t.Fatalf("parsePullArgs(-h) error = %v, want nil", err)
	}
	if pos != nil {
		t.Fatalf("parsePullArgs(-h) pos = %#v, want nil", pos)
	}
	if opts.DryRun || opts.JSON || opts.FetchManifest || opts.VerifyBlobs || opts.AllPlatforms || opts.Resume || opts.As != "" || opts.ManifestPath != "" || opts.ManifestOut != "" || opts.IndexOut != "" || opts.ManifestDir != "" || opts.Platform != "" {
		t.Fatalf("parsePullArgs(-h) opts = %#v, want zero", opts)
	}
}

func TestParsePullArgsRejectsUnknownFlag(t *testing.T) {
	_, _, err := parsePullArgs([]string{"--bogus"}, ioDiscard{})
	if err == nil {
		t.Fatal("parsePullArgs(--bogus) error = nil, want flag parse error")
	}
}

func TestPrintPullUsageShowsFlagsBeforeArgs(t *testing.T) {
	var b strings.Builder
	printPullUsage(&b)
	if !strings.Contains(b.String(), "Usage: cove pull [flags] <ref>") {
		t.Fatalf("usage = %q", b.String())
	}
	if !strings.Contains(b.String(), "--resume") {
		t.Fatalf("usage = %q, want --resume", b.String())
	}
}

func TestParsePullArgsFetchManifest(t *testing.T) {
	opts, pos, err := parsePullArgs([]string{
		"registry.example/cove/vm:latest",
		"--dry-run",
		"--fetch-manifest",
		"--verify-blobs",
		"--all-platforms",
		"--manifest-out", "selected.json",
		"--index-out", "index.json",
		"--manifest-dir", "bundle",
		"--json",
		"--platform", "linux/arm64",
	}, ioDiscard{})
	if err != nil {
		t.Fatalf("parsePullArgs fetch manifest: %v", err)
	}
	if !opts.DryRun || !opts.FetchManifest || !opts.VerifyBlobs || !opts.AllPlatforms || !opts.JSON || opts.ManifestOut != "selected.json" || opts.IndexOut != "index.json" || opts.ManifestDir != "bundle" || opts.Platform != "linux/arm64" {
		t.Fatalf("opts = %#v, want dry-run/fetch-manifest/verify-blobs/all-platforms/json/platform", opts)
	}
	if strings.Join(pos, ",") != "registry.example/cove/vm:latest" {
		t.Fatalf("pos = %#v", pos)
	}
}

func TestParsePullArgsAllowsTrailingFlags(t *testing.T) {
	opts, pos, err := parsePullArgs([]string{
		"registry.example/cove/vm:latest",
		"--dry-run",
		"--json",
		"--resume",
		"--as", "vm",
		"--manifest=manifest.json",
		"--platform", "darwin/arm64",
	}, ioDiscard{})
	if err != nil {
		t.Fatalf("parsePullArgs trailing flags: %v", err)
	}
	if !opts.DryRun || !opts.JSON || !opts.Resume || opts.As != "vm" || opts.ManifestPath != "manifest.json" || opts.Platform != "darwin/arm64" {
		t.Fatalf("opts = %#v, want dry-run/as/manifest", opts)
	}
	if strings.Join(pos, ",") != "registry.example/cove/vm:latest" {
		t.Fatalf("pos = %#v", pos)
	}
}

func TestHandlePullUsageShowsFlagsBeforeArgs(t *testing.T) {
	err := handlePull(commandTestEnv(), []string{"one", "two"})
	if err == nil || !strings.Contains(err.Error(), "usage: cove pull [flags] <ref>") {
		t.Fatalf("handlePull usage error = %v, want flags-before-args usage", err)
	}
}

func TestBuildPullPlanRejectsInvalidRef(t *testing.T) {
	_, err := buildPullPlan("me/dev-vm", pullOptions{DryRun: true})
	if err == nil || !strings.Contains(err.Error(), "invalid ref") {
		t.Fatalf("buildPullPlan() error = %v, want invalid ref", err)
	}
}

func TestBuildPullPlanRejectsIncompleteTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	vmPath := filepath.Join(home, ".vz", "vms", "dev-vm")
	if err := os.MkdirAll(vmPath, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(vmPath, "disk.img.partial"), []byte("partial"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := buildPullPlan("ghcr.io/me/dev-vm:v1", pullOptions{DryRun: true})
	if err == nil || !strings.Contains(err.Error(), "pull was interrupted") {
		t.Fatalf("buildPullPlan() error = %v, want incomplete disk", err)
	}
}

func TestBuildPullPlanResumeAllowsIncompleteTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	vmPath := filepath.Join(home, ".vz", "vms", "dev-vm")
	if err := os.MkdirAll(vmPath, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(vmPath, "disk.img.partial"), []byte("partial"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := buildPullPlan("ghcr.io/me/dev-vm:v1", pullOptions{DryRun: true, Resume: true}); err != nil {
		t.Fatalf("buildPullPlan(resume): %v", err)
	}
}

func writePullTestManifest(t *testing.T) string {
	t.Helper()

	return writePullManifest(t, pullTestManifest(t))
}

func writePullManifest(t *testing.T, manifest ociimage.Manifest) string {
	t.Helper()

	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func pullTestManifest(t *testing.T) ociimage.Manifest {
	t.Helper()

	manifest, _, err := ociimage.BuildManifest(ociimage.ManifestOptions{
		UploadTime: "2026-04-23T00:00:00Z",
		DiskSize:   3,
		Chunks: []ociimage.Chunk{
			{Index: 0, Offset: 0, Size: 3, Digest: pushTestDigest([]byte{1, 2, 3})},
		},
	})
	if err != nil {
		t.Fatalf("BuildManifest(): %v", err)
	}
	return manifest
}

func pullCompressedTestManifest(t *testing.T, disk []byte) (ociimage.Manifest, map[string][]byte) {
	t.Helper()

	manifest, blobs, _ := pullCompressedChunkedTestManifest(t, disk, int64(len(disk)))
	return manifest, blobs
}

func pullCompressedChunkedTestManifest(t *testing.T, disk []byte, chunkSize int64) (ociimage.Manifest, map[string][]byte, map[string]bool) {
	t.Helper()

	chunks, err := ociimage.DescribeChunks(bytes.NewReader(disk), chunkSize)
	if err != nil {
		t.Fatalf("DescribeChunks() error = %v", err)
	}
	layers := make([]ociimage.Descriptor, 0, len(chunks)+3)
	blobs := map[string][]byte{}
	diskDigests := map[string]bool{}
	for _, chunk := range chunks {
		prepared, err := ociimage.PrepareChunkLayer(bytes.NewReader(disk), chunk, len(chunks), false)
		if err != nil {
			t.Fatalf("PrepareChunkLayer(): %v", err)
		}
		if prepared.SkipUpload {
			layers = append(layers, ociimage.Descriptor{
				MediaType:   ociimage.MediaTypeLayer,
				Size:        0,
				Digest:      chunk.Digest,
				Annotations: ociimage.ChunkLayerAnnotations(chunk, len(chunks)),
			})
			continue
		}
		layers = append(layers, prepared.Descriptor)
		blobs[prepared.Descriptor.Digest] = prepared.Data
		diskDigests[prepared.Descriptor.Digest] = true
	}
	for _, blob := range []struct {
		role string
		data []byte
	}{
		{role: "nvram", data: []byte("aux")},
		{role: "hw-model", data: []byte("hw")},
		{role: "machine-id", data: []byte("machine")},
	} {
		desc := ociimage.Descriptor{
			MediaType: ociimage.MediaTypeLayer,
			Size:      int64(len(blob.data)),
			Digest:    pushTestDigest(blob.data),
			Annotations: map[string]string{
				ociimage.CoveRole: blob.role,
			},
		}
		layers = append(layers, desc)
		blobs[desc.Digest] = blob.data
	}

	manifest := ociimage.Manifest{
		SchemaVersion: 2,
		MediaType:     ociimage.MediaTypeImageManifest,
		Annotations: map[string]string{
			ociimage.CoveUncompressedDiskSize: fmt.Sprint(len(disk)),
		},
		Layers: layers,
	}
	return manifest, blobs, diskDigests
}

func pullTestRegistry(t *testing.T, manifest ociimage.Manifest, blobs map[string][]byte) *httptest.Server {
	t.Helper()
	manifestData, manifestDigest := pullTestManifestData(t, manifest)

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/me/dev-vm/manifests/v1":
			if r.Method != http.MethodGet {
				t.Fatalf("manifest method = %s, want GET", r.Method)
			}
			w.Header().Set("Docker-Content-Digest", manifestDigest)
			_, _ = w.Write(manifestData)
		default:
			const prefix = "/v2/me/dev-vm/blobs/"
			if !strings.HasPrefix(r.URL.Path, prefix) {
				t.Fatalf("path = %q", r.URL.Path)
			}
			if r.Method != http.MethodGet {
				t.Fatalf("blob method = %s, want GET", r.Method)
			}
			digest := strings.TrimPrefix(r.URL.Path, prefix)
			data, ok := blobs[digest]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(data)
		}
	}))
}

func pullCountingRegistry(t *testing.T, manifest ociimage.Manifest, blobs map[string][]byte, diskDigests map[string]bool, diskGets *atomic.Int32) *httptest.Server {
	t.Helper()
	manifestData, manifestDigest := pullTestManifestData(t, manifest)

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/me/dev-vm/manifests/v1":
			w.Header().Set("Docker-Content-Digest", manifestDigest)
			_, _ = w.Write(manifestData)
		default:
			const prefix = "/v2/me/dev-vm/blobs/"
			if !strings.HasPrefix(r.URL.Path, prefix) {
				t.Fatalf("path = %q", r.URL.Path)
			}
			digest := strings.TrimPrefix(r.URL.Path, prefix)
			if diskDigests[digest] {
				diskGets.Add(1)
			}
			data, ok := blobs[digest]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(data)
		}
	}))
}

func writePullBaseVM(t *testing.T, home, name, provenance string, disk []byte) {
	t.Helper()
	vmDir := filepath.Join(home, ".vz", "vms", name)
	if err := os.MkdirAll(vmDir, 0755); err != nil {
		t.Fatalf("MkdirAll(base VM): %v", err)
	}
	if err := os.WriteFile(filepath.Join(vmDir, "disk.img"), disk, 0600); err != nil {
		t.Fatalf("WriteFile(base disk): %v", err)
	}
	if err := os.WriteFile(filepath.Join(vmDir, "disk.provenance"), []byte(provenance+"\n"), 0644); err != nil {
		t.Fatalf("WriteFile(base provenance): %v", err)
	}
}

func pullTestManifestData(t *testing.T, manifest ociimage.Manifest) ([]byte, string) {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return data, digestData(data)
}
