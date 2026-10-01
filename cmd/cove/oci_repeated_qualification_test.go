package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tmc/cove/internal/ociimage"
	"github.com/tmc/cove/internal/store"
)

type qualificationRegistry struct {
	mu             sync.Mutex
	gets           map[string]int
	blobs          map[string][]byte
	manifest       []byte
	manifestDigest string
	before         func(*http.Request)
}

func (q *qualificationRegistry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.URL.Path, "/manifests/") {
		w.Header().Set("Docker-Content-Digest", q.manifestDigest)
		_, _ = w.Write(q.manifest)
		return
	}
	_, digest, ok := strings.Cut(r.URL.Path, "/blobs/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	q.mu.Lock()
	q.gets[digest]++
	body, found := q.blobs[digest]
	before := q.before
	q.mu.Unlock()
	if before != nil {
		before(r)
	}
	if !found {
		http.NotFound(w, r)
		return
	}
	_, _ = w.Write(body)
}

func (q *qualificationRegistry) count(digest string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.gets[digest]
}

func TestPullRepeatedChunksColdWarmAndStaleCache(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	want := []byte("aaaabbbbaaaaccccaaaa")
	manifest, blobs, digests := pullCompressedChunkedTestManifest(t, want, 4)
	data, digest := pullTestManifestData(t, manifest)
	registry := &qualificationRegistry{gets: map[string]int{}, blobs: blobs, manifest: data, manifestDigest: digest}
	server := httptest.NewServer(registry)
	defer server.Close()
	opts := pullOptions{RegistryBaseURL: server.URL, StoreDir: filepath.Join(root, "store")}
	plan, err := buildPullPlan("ghcr.io/me/dev-vm:v1", opts)
	if err != nil {
		t.Fatal(err)
	}
	repeated := plan.Manifest.DiskLayers[0].Descriptor.Digest
	if repeated != plan.Manifest.DiskLayers[2].Descriptor.Digest || repeated != plan.Manifest.DiskLayers[4].Descriptor.Digest {
		t.Fatal("fixture lacks nonadjacent duplicate chunks")
	}
	if err := pullDisk(context.Background(), plan, opts); err != nil {
		t.Fatal(err)
	}
	assertQualifiedDisk(t, plan.VMDir, want)
	for digest := range digests {
		n := registry.count(digest)
		if n < 1 || n > 3 {
			t.Fatalf("cold fetch count %s = %d", digest, n)
		}
		t.Logf("cold compressed digest %s fetched %d time(s)", digest, n)
	}
	before := registry.count(repeated)
	plan.VMDir = filepath.Join(root, "warm")
	plan.VMName = "warm"
	if err := pullDisk(context.Background(), plan, opts); err != nil {
		t.Fatal(err)
	}
	assertQualifiedDisk(t, plan.VMDir, want)
	if n := registry.count(repeated); n != before {
		t.Fatalf("warm repeated digest fetched again: %d -> %d", before, n)
	}
	cached := filepath.Join(opts.StoreDir, "blobs", "sha256", strings.TrimPrefix(repeated, "sha256:"))
	if err := os.WriteFile(cached, []byte("stale invalid cache bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	plan.VMDir = filepath.Join(root, "stale")
	plan.VMName = "stale"
	if err := pullDisk(context.Background(), plan, opts); err != nil {
		t.Fatal(err)
	}
	assertQualifiedDisk(t, plan.VMDir, want)
	if registry.count(repeated) <= before {
		t.Fatal("stale cached blob was not refetched")
	}
	if err := store.New(opts.StoreDir).VerifyBlob(repeated, plan.Manifest.DiskLayers[0].Descriptor.Size); err != nil {
		t.Fatal(err)
	}
}

func assertQualifiedDisk(t *testing.T, dir string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, "disk.img"))
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("disk = %q, want %q: %v", got, want, err)
	}
}

func TestLumeRepeatedPartsPreserveOccurrences(t *testing.T) {
	bodies := [][]byte{[]byte("same"), []byte("different"), []byte("same"), []byte("last"), []byte("same")}
	parts, blobs := lumeTestParts(bodies)
	registry := &qualificationRegistry{gets: map[string]int{}, blobs: blobs}
	server := httptest.NewServer(registry)
	defer server.Close()
	var out bytes.Buffer
	if err := lumeFeedTarStream(context.Background(), ociimage.RegistryClient{BaseURL: server.URL}, lumeTestPlan(t, parts), &out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), bytes.Join(bodies, nil)) {
		t.Fatalf("ordered parts = %q", out.Bytes())
	}
	if n := registry.count(parts[0].Descriptor.Digest); n != 3 {
		t.Fatalf("repeated part fetches = %d, want 3 occurrences", n)
	}
	t.Log("Lume currently fetches each repeated part occurrence; no persistent part cache reuse is claimed")
}

func qualifiedTartLayers(t *testing.T, bodies [][]byte) ([]ociimage.TartDiskLayer, map[string][]byte) {
	t.Helper()
	var layers []ociimage.TartDiskLayer
	blobs := map[string][]byte{}
	offset := int64(0)
	for _, body := range bodies {
		compressed, err := ociimage.CompressAppleLZ4(body)
		if err != nil {
			t.Fatal(err)
		}
		desc := ociimage.Descriptor{Digest: digestBytes(compressed), Size: int64(len(compressed)), MediaType: ociimage.TartDiskV2MediaType}
		layers = append(layers, ociimage.TartDiskLayer{Descriptor: desc, UncompressedSize: int64(len(body)), UncompressedContentDigest: digestBytes(body), Offset: offset})
		blobs[desc.Digest] = compressed
		offset += int64(len(body))
	}
	return layers, blobs
}

func TestTartRepeatedChunksPreserveOffsets(t *testing.T) {
	bodies := [][]byte{[]byte("same"), []byte("different"), []byte("same"), []byte("last"), []byte("same")}
	layers, blobs := qualifiedTartLayers(t, bodies)
	registry := &qualificationRegistry{gets: map[string]int{}, blobs: blobs}
	server := httptest.NewServer(registry)
	defer server.Close()
	ref, err := ociimage.ParseReference("example.com/me/dev-vm:latest")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	disk, err := os.Create(filepath.Join(dir, "disk.img"))
	if err != nil {
		t.Fatal(err)
	}
	defer disk.Close()
	plan := &pullPlan{Ref: ref, Manifest: ociimage.ParsedManifest{Tart: ociimage.TartManifest{DiskLayers: layers}}}
	if err := tartPullDiskLayers(context.Background(), ociimage.RegistryClient{BaseURL: server.URL}, plan, disk); err != nil {
		t.Fatal(err)
	}
	assertQualifiedDisk(t, dir, bytes.Join(bodies, nil))
	if n := registry.count(layers[0].Descriptor.Digest); n != 3 {
		t.Fatalf("repeated Tart fetches = %d, want 3", n)
	}
	t.Log("Tart currently fetches each repeated layer occurrence; no persistent layer cache reuse is claimed")
}

func TestTartCompressedDescriptorIntegrity(t *testing.T) {
	for _, kind := range []string{"digest", "size", "compressed short size", "uncompressed digest", "uncompressed size"} {
		t.Run(kind, func(t *testing.T) {
			layers, blobs := qualifiedTartLayers(t, [][]byte{[]byte("valid decompressed disk")})
			layer := layers[0]
			if kind == "digest" {
				compressed := blobs[layer.Descriptor.Digest]
				layer.Descriptor.Digest = digestBytes([]byte("wrong compressed identity"))
				blobs = map[string][]byte{layer.Descriptor.Digest: compressed}
			} else if kind == "size" {
				layer.Descriptor.Size++
			} else if kind == "compressed short size" {
				layer.Descriptor.Size--
			} else if kind == "uncompressed digest" {
				layer.UncompressedContentDigest = digestBytes([]byte("wrong decoded bytes"))
			} else {
				layer.UncompressedSize++
			}
			registry := &qualificationRegistry{gets: map[string]int{}, blobs: blobs}
			server := httptest.NewServer(registry)
			defer server.Close()
			ref, err := ociimage.ParseReference("example.com/me/dev-vm:latest")
			if err != nil {
				t.Fatal(err)
			}
			disk, err := os.Create(filepath.Join(t.TempDir(), "partial"))
			if err != nil {
				t.Fatal(err)
			}
			defer disk.Close()
			err = tartPullDiskLayer(context.Background(), ociimage.RegistryClient{BaseURL: server.URL}, ref, disk, layer)
			check := "size"
			if kind == "uncompressed size" {
				check = "decompressed"
			}
			if strings.Contains(kind, "digest") {
				check = "digest"
			}
			if err == nil || !strings.Contains(err.Error(), check) {
				t.Fatalf("compressed %s violation error = %v", kind, err)
			}
			stat, err := disk.Stat()
			if err != nil || stat.Size() != 0 {
				t.Fatalf("bad blob wrote disk bytes: %v, %v", stat, err)
			}
		})
	}
}

func TestPullRepeatedChunksCancellationResume(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	want := []byte("aaaabbbbaaaaccccaaaa")
	manifest, blobs, diskDigests := pullCompressedChunkedTestManifest(t, want, 4)
	data, digest := pullTestManifestData(t, manifest)
	started := make(chan struct{})
	var once sync.Once
	registry := &qualificationRegistry{gets: map[string]int{}, blobs: blobs, manifest: data, manifestDigest: digest, before: func(r *http.Request) {
		_, digest, _ := strings.Cut(r.URL.Path, "/blobs/")
		if diskDigests[digest] {
			once.Do(func() { close(started) })
			<-r.Context().Done()
		}
	}}
	server := httptest.NewServer(registry)
	defer server.Close()
	opts := pullOptions{RegistryBaseURL: server.URL, StoreDir: filepath.Join(root, "store")}
	plan, err := buildPullPlan("ghcr.io/me/dev-vm:v1", opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- pullDisk(ctx, plan, opts) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("fetch did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled pull succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pull cancellation did not finish")
	}
	for _, name := range []string{"disk.img", "disk.provenance"} {
		if _, err := os.Stat(filepath.Join(plan.VMDir, name)); !os.IsNotExist(err) {
			t.Fatalf("canceled pull published %s: %v", name, err)
		}
	}
	registry.mu.Lock()
	registry.before = nil
	registry.mu.Unlock()
	opts.Resume = true
	if err := pullDisk(context.Background(), plan, opts); err != nil {
		t.Fatal(err)
	}
	assertQualifiedDisk(t, plan.VMDir, want)
}

func qualifiedLumeDiskParts(t *testing.T, want []byte) ([]ociimage.LumeLayer, map[string][]byte) {
	t.Helper()
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	if err := tw.WriteHeader(&tar.Header{Name: "disk.img", Mode: 0600, Size: int64(len(want))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(want); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	data := archive.Bytes()
	rawParts := [][]byte{data[:512]}
	for offset := 0; offset < len(want); offset += 4 {
		rawParts = append(rawParts, data[512+offset:512+min(offset+4, len(want))])
	}
	rawParts = append(rawParts, data[512+len(want):])
	var bodies [][]byte
	for _, raw := range rawParts {
		var out bytes.Buffer
		gz := gzip.NewWriter(&out)
		if _, err := gz.Write(raw); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, out.Bytes())
	}
	return lumeTestParts(bodies)
}

func TestLumeRepeatedDiskPartsCancellationRetry(t *testing.T) {
	want := []byte("aaaabbbbaaaaccccaaaa")
	parts, blobs := qualifiedLumeDiskParts(t, want)
	registry := &qualificationRegistry{gets: map[string]int{}, blobs: blobs}
	server := httptest.NewServer(registry)
	defer server.Close()
	plan := lumeTestPlan(t, parts)
	plan.ManifestDigest = digestBytes([]byte("lume manifest"))
	started := make(chan struct{})
	var once sync.Once
	registry.before = func(r *http.Request) { once.Do(func() { close(started) }); <-r.Context().Done() }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- lumePullDisk(ctx, plan, pullOptions{RegistryBaseURL: server.URL}) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("part fetch did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled Lume pull succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Lume cancellation did not finish")
	}
	for _, name := range []string{"disk.img", "disk.img.partial", "disk.provenance"} {
		if _, err := os.Stat(filepath.Join(plan.VMDir, name)); !os.IsNotExist(err) {
			t.Fatalf("canceled Lume pull retained %s: %v", name, err)
		}
	}
	entries, err := os.ReadDir(plan.VMDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".lume-parts-") {
			t.Fatal("part cache retained after cancellation")
		}
	}
	registry.mu.Lock()
	registry.before = nil
	registry.mu.Unlock()
	before := registry.count(parts[1].Descriptor.Digest)
	if err := lumePullDisk(context.Background(), plan, pullOptions{RegistryBaseURL: server.URL}); err != nil {
		t.Fatal(err)
	}
	assertQualifiedDisk(t, plan.VMDir, want)
	if n := registry.count(parts[1].Descriptor.Digest) - before; n != 3 {
		t.Fatalf("nonadjacent repeated gzip part fetches=%d, want 3", n)
	}
}

func TestTartRepeatedDiskCancellationRetry(t *testing.T) {
	want := []byte("aaaabbbbaaaaccccaaaa")
	bodies := [][]byte{want[:4], want[4:8], want[8:12], want[12:16], want[16:]}
	layers, blobs := qualifiedTartLayers(t, bodies)
	config, nvram := []byte(`{"cpuCount":4,"memorySize":4294967296}`), []byte("firmware bytes")
	configDesc := ociimage.Descriptor{Digest: digestBytes(config), Size: int64(len(config)), MediaType: ociimage.TartConfigMediaType}
	nvramDesc := ociimage.Descriptor{Digest: digestBytes(nvram), Size: int64(len(nvram)), MediaType: ociimage.TartNVRAMMediaType}
	blobs[configDesc.Digest] = config
	blobs[nvramDesc.Digest] = nvram
	started := make(chan struct{})
	var once sync.Once
	registry := &qualificationRegistry{gets: map[string]int{}, blobs: blobs, before: func(r *http.Request) {
		_, digest, _ := strings.Cut(r.URL.Path, "/blobs/")
		if digest != configDesc.Digest && digest != nvramDesc.Digest {
			once.Do(func() { close(started) })
			<-r.Context().Done()
		}
	}}
	server := httptest.NewServer(registry)
	defer server.Close()
	ref, err := ociimage.ParseReference("example.com/me/dev-vm:latest")
	if err != nil {
		t.Fatal(err)
	}
	plan := &pullPlan{Ref: ref, VMDir: t.TempDir(), ManifestDigest: digestBytes([]byte("tart manifest")), Manifest: ociimage.ParsedManifest{Tart: ociimage.TartManifest{DiskLayers: layers, ConfigLayer: configDesc, NVRAMLayer: nvramDesc, UncompressedDiskSize: int64(len(want))}}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- tartPullDisk(ctx, plan, pullOptions{RegistryBaseURL: server.URL}) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("Tart fetch did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled Tart pull succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Tart cancellation did not finish")
	}
	for _, name := range []string{"disk.img", "disk.provenance"} {
		if _, err := os.Stat(filepath.Join(plan.VMDir, name)); !os.IsNotExist(err) {
			t.Fatalf("canceled Tart pull published %s: %v", name, err)
		}
	}
	registry.mu.Lock()
	registry.before = nil
	registry.mu.Unlock()
	before := registry.count(layers[0].Descriptor.Digest)
	if err := tartPullDisk(context.Background(), plan, pullOptions{RegistryBaseURL: server.URL, Resume: true}); err != nil {
		t.Fatal(err)
	}
	assertQualifiedDisk(t, plan.VMDir, want)
	if got := mustReadFile(t, filepath.Join(plan.VMDir, "tart-config.json")); !bytes.Equal(got, config) {
		t.Fatal("Tart source config not preserved")
	}
	if got := mustReadFile(t, filepath.Join(plan.VMDir, "aux.img")); !bytes.Equal(got, nvram) {
		t.Fatal("Tart firmware bytes not preserved")
	}
	if n := registry.count(layers[0].Descriptor.Digest) - before; n != 3 {
		t.Fatalf("retry repeated fetches=%d, want 3", n)
	}
}

func TestLumePullWaitsForAllPartVerification(t *testing.T) {
	want := []byte("disk data")
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "disk.img", Mode: 0600, Size: int64(len(want))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(want); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	parts, blobs := lumeTestParts([][]byte{archive.Bytes(), []byte("unverified trailing part")})
	trailing := parts[1].Descriptor.Digest
	registry := &qualificationRegistry{gets: map[string]int{}, blobs: blobs, before: func(r *http.Request) {
		if strings.HasSuffix(r.URL.Path, trailing) {
			<-r.Context().Done()
		}
	}}
	server := httptest.NewServer(registry)
	defer server.Close()
	plan := lumeTestPlan(t, parts)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := lumePullDisk(ctx, plan, pullOptions{RegistryBaseURL: server.URL})
	if err == nil {
		t.Fatal("published disk before all declared parts were verified")
	}
	if _, err := os.Stat(filepath.Join(plan.VMDir, "disk.img")); !os.IsNotExist(err) {
		t.Fatalf("unverified import published disk: %v", err)
	}
}

func TestPullRepeatedChunksRejectBadDescriptor(t *testing.T) {
	for _, kind := range []string{"digest", "size"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("HOME", root)
			manifest, blobs, _ := pullCompressedChunkedTestManifest(t, []byte("aaaabbbbaaaa"), 4)
			if kind == "digest" {
				old := manifest.Layers[0].Digest
				manifest.Layers[0].Digest = digestBytes([]byte("wrong compressed blob"))
				blobs[manifest.Layers[0].Digest] = blobs[old]
			} else {
				manifest.Layers[0].Size++
			}
			data, digest := pullTestManifestData(t, manifest)
			registry := &qualificationRegistry{gets: map[string]int{}, blobs: blobs, manifest: data, manifestDigest: digest}
			server := httptest.NewServer(registry)
			defer server.Close()
			opts := pullOptions{RegistryBaseURL: server.URL, StoreDir: filepath.Join(root, "store")}
			plan, err := buildPullPlan("ghcr.io/me/dev-vm:v1", opts)
			if err != nil {
				t.Fatal(err)
			}
			err = pullDisk(context.Background(), plan, opts)
			if err == nil || !strings.Contains(err.Error(), kind) {
				t.Fatalf("bad %s error = %v", kind, err)
			}
			for _, name := range []string{"disk.img", "disk.provenance"} {
				if _, err := os.Stat(filepath.Join(plan.VMDir, name)); !os.IsNotExist(err) {
					t.Fatalf("invalid native pull published %s: %v", name, err)
				}
			}
		})
	}
}

func TestLumePullRejectsGzipTrailerFailure(t *testing.T) {
	for _, kind := range []string{"checksum", "truncated"} {
		t.Run(kind, func(t *testing.T) {
			parts, blobs := qualifiedLumeDiskParts(t, []byte("aaaabbbbaaaa"))
			last := len(parts) - 1
			body := append([]byte(nil), blobs[parts[last].Descriptor.Digest]...)
			if kind == "checksum" {
				body[len(body)-8] ^= 1
			} else {
				body = body[:len(body)-8]
			}
			parts[last].Descriptor.Digest = digestBytes(body)
			parts[last].Descriptor.Size = int64(len(body))
			blobs[parts[last].Descriptor.Digest] = body
			registry := &qualificationRegistry{gets: map[string]int{}, blobs: blobs}
			server := httptest.NewServer(registry)
			defer server.Close()
			plan := lumeTestPlan(t, parts)
			err := lumePullDisk(context.Background(), plan, pullOptions{RegistryBaseURL: server.URL})
			if err == nil {
				t.Fatal("invalid gzip trailer succeeded")
			}
			if _, err := os.Stat(filepath.Join(plan.VMDir, "disk.img")); !os.IsNotExist(err) {
				t.Fatalf("invalid gzip published disk: %v", err)
			}
		})
	}
}
