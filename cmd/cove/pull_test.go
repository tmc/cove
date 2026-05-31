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
		"--manifest", "manifest.json",
		"ghcr.io/me/dev-vm:v1",
	}, ioDiscard{})
	if err != nil {
		t.Fatalf("parsePullArgs(): %v", err)
	}
	if !opts.DryRun || !opts.Resume || opts.As != "local-dev" || opts.ManifestPath != "manifest.json" {
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
	if opts.DryRun || opts.Resume || opts.As != "" || opts.ManifestPath != "" {
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

func TestParsePullArgsAllowsTrailingFlags(t *testing.T) {
	opts, pos, err := parsePullArgs([]string{
		"registry.example/cove/vm:latest",
		"--dry-run",
		"--resume",
		"--as", "vm",
		"--manifest=manifest.json",
	}, ioDiscard{})
	if err != nil {
		t.Fatalf("parsePullArgs trailing flags: %v", err)
	}
	if !opts.DryRun || !opts.Resume || opts.As != "vm" || opts.ManifestPath != "manifest.json" {
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
