package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/vz-macos/internal/ociimage"
)

// TestPullDispatch_LumeManifest serves a lume tar-split manifest and asserts
// that buildPullPlan — which feeds handlePull's format switch — classifies
// it as FormatLume and populates Lume.DiskParts. This is the path that
// dispatches into lumePullDisk in pull.go.
func TestPullDispatch_LumeManifest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	manifest := newLumeMockManifest()
	srv := newOCIDispatchRegistry(t, manifest)
	t.Cleanup(srv.Close)

	plan, err := buildPullPlan("ghcr.io/me/dev-vm:v1", pullOptions{
		DryRun:          true,
		RegistryBaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("buildPullPlan(): %v", err)
	}
	if plan.Manifest.Format != ociimage.FormatLume {
		t.Fatalf("Format = %v, want FormatLume", plan.Manifest.Format)
	}
	if len(plan.Manifest.Lume.DiskParts) != 2 {
		t.Fatalf("DiskParts = %d, want 2", len(plan.Manifest.Lume.DiskParts))
	}
	if plan.Manifest.Lume.NvramLayer == nil {
		t.Error("NvramLayer = nil; want nvram.bin sidecar")
	}
	if plan.Manifest.Lume.ConfigLayer == nil {
		t.Error("ConfigLayer = nil; want config.json sidecar")
	}
	if len(plan.Manifest.Chunks) != 0 {
		t.Errorf("Chunks = %d, want 0 (lume layout has no cove chunks)", len(plan.Manifest.Chunks))
	}
}

// TestPullDispatch_CoveManifest serves a cove LZ4-chunked manifest and
// asserts buildPullPlan classifies it as FormatCove. This is the default
// branch in handlePull's format switch.
func TestPullDispatch_CoveManifest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	manifest := pullTestManifest(t)
	srv := newOCIDispatchRegistry(t, manifest)
	t.Cleanup(srv.Close)

	plan, err := buildPullPlan("ghcr.io/me/dev-vm:v1", pullOptions{
		DryRun:          true,
		RegistryBaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("buildPullPlan(): %v", err)
	}
	if plan.Manifest.Format != ociimage.FormatCove {
		t.Fatalf("Format = %v, want FormatCove", plan.Manifest.Format)
	}
	if len(plan.Manifest.Chunks) == 0 {
		t.Error("Chunks empty; cove manifest should populate disk chunks")
	}
	if len(plan.Manifest.Lume.DiskParts) != 0 {
		t.Errorf("Lume.DiskParts = %d, want 0 (cove layout has no lume parts)", len(plan.Manifest.Lume.DiskParts))
	}
}

// TestPullDispatch_LumeReverseTrip drives the full export→serve→fetch→parse
// loop: build a manifest with the production exporter (buildLumeManifest),
// serve it through the mock registry, pull it via buildPullPlan, then
// re-parse the wire JSON with ociimage.ParseLumeManifest. Confirms the
// exporter's output is what the importer's lume parser consumes.
func TestPullDispatch_LumeReverseTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Build a real lume manifest using the same code path cove push --format
	// lume --dry-run runs. We feed buildLumeManifest synthetic parts because
	// driving buildLumePushPlan would require a fully-staged VM directory
	// (disk.img + aux.img + vmconfig) — out of scope for an HTTP dispatch test.
	plan := &lumePushPlan{
		ConfigJSON:  []byte(`{"os":"macos","cpuCount":4,"memorySize":4294967296}`),
		NvramSize:   1024,
		NvramDigest: "sha256:" + strings.Repeat("d", 64),
		UploadTime:  "2026-04-25T12:00:00Z",
		Parts: []lumePushPart{
			{Number: 1, Title: "disk.img.part.aa", Size: 100, Digest: "sha256:" + strings.Repeat("e", 64),
				MediaType: ociimage.LumeTarLayerMediaTypePrefix + ";part.number=1;part.total=2"},
			{Number: 2, Title: "disk.img.part.ab", Size: 50, Digest: "sha256:" + strings.Repeat("f", 64),
				MediaType: ociimage.LumeTarLayerMediaTypePrefix + ";part.number=2;part.total=2"},
		},
	}
	manifest := buildLumeManifest(plan, plan.ConfigJSON)

	srv := newOCIDispatchRegistry(t, manifest)
	t.Cleanup(srv.Close)

	pulled, err := buildPullPlan("ghcr.io/me/dev-vm:v1", pullOptions{
		DryRun:          true,
		RegistryBaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("buildPullPlan(): %v", err)
	}
	if pulled.Manifest.Format != ociimage.FormatLume {
		t.Fatalf("Format = %v, want FormatLume", pulled.Manifest.Format)
	}
	if got, want := len(pulled.Manifest.Lume.DiskParts), len(plan.Parts); got != want {
		t.Fatalf("DiskParts = %d, want %d", got, want)
	}
	for i, got := range pulled.Manifest.Lume.DiskParts {
		want := plan.Parts[i]
		if got.PartNumber != want.Number {
			t.Errorf("part %d PartNumber = %d, want %d", i, got.PartNumber, want.Number)
		}
		if got.Title != want.Title {
			t.Errorf("part %d Title = %q, want %q", i, got.Title, want.Title)
		}
		if got.Descriptor.Digest != want.Digest {
			t.Errorf("part %d Digest = %q, want %q", i, got.Descriptor.Digest, want.Digest)
		}
	}
}

// newLumeMockManifest constructs a minimal lume tar-split manifest:
// schemaVersion=2, OCI image manifest mediaType, no cove annotations,
// two tar-split disk parts (aa, ab) addressed by org.opencontainers.image.title,
// plus nvram.bin and config.json sidecars.
func newLumeMockManifest() ociimage.Manifest {
	emptyConfigDigest := "sha256:44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a" // sha256("{}")
	return ociimage.Manifest{
		SchemaVersion: 2,
		MediaType:     ociimage.MediaTypeImageManifest,
		Config: ociimage.Descriptor{
			MediaType: "application/vnd.oci.empty.v1+json",
			Size:      2,
			Digest:    emptyConfigDigest,
		},
		Layers: []ociimage.Descriptor{
			{
				MediaType:   ociimage.LumeTarLayerMediaTypePrefix + ";part.number=1;part.total=2",
				Size:        128,
				Digest:      "sha256:" + strings.Repeat("a", 64),
				Annotations: map[string]string{"org.opencontainers.image.title": "disk.img.part.aa"},
			},
			{
				MediaType:   ociimage.LumeTarLayerMediaTypePrefix + ";part.number=2;part.total=2",
				Size:        64,
				Digest:      "sha256:" + strings.Repeat("b", 64),
				Annotations: map[string]string{"org.opencontainers.image.title": "disk.img.part.ab"},
			},
			{
				MediaType:   ociimage.MediaTypeImageConfig,
				Size:        32,
				Digest:      "sha256:" + strings.Repeat("c", 64),
				Annotations: map[string]string{"org.opencontainers.image.title": ociimage.LumeConfigTitle},
			},
			{
				MediaType:   ociimage.MediaTypeLayer,
				Size:        1024,
				Digest:      "sha256:" + strings.Repeat("d", 64),
				Annotations: map[string]string{"org.opencontainers.image.title": ociimage.LumeNvramTitle},
			},
		},
		Annotations: map[string]string{
			"org.opencontainers.image.created": "2026-04-25T12:00:00Z",
		},
	}
}

// newOCIDispatchRegistry returns an httptest.Server that serves manifest at
// the standard OCI manifest path. Blob requests 404 — these tests only
// exercise the manifest fetch + format dispatch, not blob streaming.
func newOCIDispatchRegistry(t *testing.T, manifest ociimage.Manifest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/me/dev-vm/manifests/v1":
			if r.Method != http.MethodGet {
				t.Errorf("manifest method = %s, want GET", r.Method)
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", ociimage.MediaTypeImageManifest)
			w.Header().Set("Docker-Content-Digest", "sha256:dispatch-test")
			if err := json.NewEncoder(w).Encode(manifest); err != nil {
				t.Errorf("encode manifest: %v", err)
			}
		case strings.HasPrefix(r.URL.Path, "/v2/me/dev-vm/blobs/"):
			http.NotFound(w, r)
		default:
			t.Errorf("unexpected request path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
}

// TestPullDispatch_TartManifest serves a synthesised tart manifest and
// asserts buildPullPlan classifies it as FormatTart and populates
// Tart.DiskLayers / Tart.NVRAMLayer / Tart.ConfigLayer. Mirrors
// TestPullDispatch_LumeManifest for the third format branch. Blob bodies
// are returned as 404 — the dispatch test only exercises the manifest
// fetch and format selector.
func TestPullDispatch_TartManifest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	manifest, _ := newTartMockManifest(t, [][]byte{{0x00, 0x00, 0x00, 0x00}})
	srv := newOCIDispatchRegistry(t, manifest)
	t.Cleanup(srv.Close)

	plan, err := buildPullPlan("ghcr.io/me/dev-vm:v1", pullOptions{
		DryRun:          true,
		RegistryBaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("buildPullPlan(): %v", err)
	}
	if plan.Manifest.Format != ociimage.FormatTart {
		t.Fatalf("Format = %v, want FormatTart", plan.Manifest.Format)
	}
	if len(plan.Manifest.Tart.DiskLayers) != 1 {
		t.Fatalf("DiskLayers = %d, want 1", len(plan.Manifest.Tart.DiskLayers))
	}
	if plan.Manifest.Tart.ConfigLayer.Digest == "" {
		t.Error("ConfigLayer not populated")
	}
	if plan.Manifest.Tart.NVRAMLayer.Digest == "" {
		t.Error("NVRAMLayer not populated")
	}
	if len(plan.Manifest.Chunks) != 0 {
		t.Errorf("cove Chunks unexpectedly populated: %d", len(plan.Manifest.Chunks))
	}
	if len(plan.Manifest.Lume.DiskParts) != 0 {
		t.Errorf("Lume DiskParts unexpectedly populated: %d", len(plan.Manifest.Lume.DiskParts))
	}
}

// TestPullDispatch_TartFullPull drives the full handlePull dispatch into
// tartPullDisk: a synthesised tart manifest with two chunks of Apple-LZ4-
// compressed disk bytes plus stub config/nvram blobs is served by httptest,
// pulled into a fresh VMDir, and the resulting disk.img is compared against
// the original concatenated payload byte-for-byte. Confirms the FormatTart
// branch end-to-end (decompress, content-digest verify, WriteAt at offset,
// atomic rename, provenance) without depending on an external registry.
func TestPullDispatch_TartFullPull(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Two chunks of disk bytes — distinct enough that an offset error would
	// be visible. Sizes are below appleLZ4MaxBlockSize so the encoder emits
	// one block per chunk.
	chunk0 := bytes.Repeat([]byte("AB"), 4096) // 8 KiB
	chunk1 := bytes.Repeat([]byte("CD"), 4096) // 8 KiB
	manifest, blobs := newTartMockManifest(t, [][]byte{chunk0, chunk1})

	srv := newOCITartBlobRegistry(t, manifest, blobs)
	t.Cleanup(srv.Close)

	// Drive pull directly against the test server. handlePull's CLI parser
	// has no --registry-base-url flag — we set it on pullOptions instead,
	// then call buildPullPlan + tartPullDisk to mirror handlePull's
	// FormatTart switch arm.
	opts := pullOptions{RegistryBaseURL: srv.URL}
	plan, err := buildPullPlan("ghcr.io/me/dev-vm:v1", opts)
	if err != nil {
		t.Fatalf("buildPullPlan(): %v", err)
	}
	if plan.Manifest.Format != ociimage.FormatTart {
		t.Fatalf("Format = %v, want FormatTart", plan.Manifest.Format)
	}
	if err := tartPullDisk(context.Background(), plan, opts); err != nil {
		t.Fatalf("tartPullDisk(): %v", err)
	}

	vmDir := filepath.Join(home, ".vz", "vms", "dev-vm")
	got, err := os.ReadFile(filepath.Join(vmDir, "disk.img"))
	if err != nil {
		t.Fatalf("ReadFile(disk.img): %v", err)
	}
	want := append(append([]byte{}, chunk0...), chunk1...)
	if !bytes.Equal(got, want) {
		t.Fatalf("disk.img = %d bytes, want %d (content mismatch)", len(got), len(want))
	}
	if _, err := os.Stat(filepath.Join(vmDir, "aux.img")); err != nil {
		t.Errorf("aux.img stat: %v", err)
	}
	if _, err := os.Stat(filepath.Join(vmDir, "tart-config.json")); err != nil {
		t.Errorf("tart-config.json stat: %v", err)
	}
	if _, err := os.Stat(filepath.Join(vmDir, "disk.img.partial")); !os.IsNotExist(err) {
		t.Errorf("partial stat error = %v, want not exist", err)
	}
	prov, err := os.ReadFile(filepath.Join(vmDir, "disk.provenance"))
	if err != nil {
		t.Fatalf("ReadFile(disk.provenance): %v", err)
	}
	if string(prov) != "sha256:dispatch-test\n" {
		t.Errorf("provenance = %q, want sha256:dispatch-test", string(prov))
	}
	cfg, err := os.ReadFile(filepath.Join(vmDir, "config.json"))
	if err != nil {
		t.Fatalf("ReadFile(config.json): %v", err)
	}
	if !strings.Contains(string(cfg), `"cpu": 4`) {
		t.Errorf("config.json missing cpu projection: %s", string(cfg))
	}
	if !strings.Contains(string(cfg), `"memoryGB": 8`) {
		t.Errorf("config.json missing memory projection: %s", string(cfg))
	}
}

// newTartMockManifest synthesises a tart manifest with one disk-v2 layer per
// chunk, an Apple-LZ4-compressed payload per layer, plus stub config and
// nvram blobs. The returned blob map is digest → raw body suitable for
// newOCITartBlobRegistry.
func newTartMockManifest(t *testing.T, chunks [][]byte) (ociimage.Manifest, map[string][]byte) {
	t.Helper()

	blobs := map[string][]byte{}
	layers := make([]ociimage.Descriptor, 0, len(chunks)+2)

	// tart config layer — a real tart VMConfig JSON shape so
	// tartWriteCoveConfig has something to project. cpuCount=4,
	// memorySize=8 GiB.
	configJSON := []byte(`{"cpuCount":4,"memorySize":8589934592}`)
	configDigest := "sha256:" + sha256Hex(configJSON)
	blobs[configDigest] = configJSON
	layers = append(layers, ociimage.Descriptor{
		MediaType: ociimage.TartConfigMediaType,
		Size:      int64(len(configJSON)),
		Digest:    configDigest,
	})

	// disk-v2 layers — one Apple-LZ4 stream per chunk.
	var totalUncompressed int64
	for _, raw := range chunks {
		comp, err := ociimage.CompressAppleLZ4(raw)
		if err != nil {
			t.Fatalf("CompressAppleLZ4(): %v", err)
		}
		compDigest := "sha256:" + sha256Hex(comp)
		blobs[compDigest] = comp
		rawDigest := "sha256:" + sha256Hex(raw)
		layers = append(layers, ociimage.Descriptor{
			MediaType: ociimage.TartDiskV2MediaType,
			Size:      int64(len(comp)),
			Digest:    compDigest,
			Annotations: map[string]string{
				ociimage.TartUncompressedSize:          fmt.Sprint(len(raw)),
				ociimage.TartUncompressedContentDigest: rawDigest,
			},
		})
		totalUncompressed += int64(len(raw))
	}

	// nvram layer — stub bytes.
	nvram := []byte("nvram-bytes")
	nvramDigest := "sha256:" + sha256Hex(nvram)
	blobs[nvramDigest] = nvram
	layers = append(layers, ociimage.Descriptor{
		MediaType: ociimage.TartNVRAMMediaType,
		Size:      int64(len(nvram)),
		Digest:    nvramDigest,
	})

	manifest := ociimage.Manifest{
		SchemaVersion: 2,
		MediaType:     ociimage.MediaTypeImageManifest,
		Layers:        layers,
		Annotations: map[string]string{
			ociimage.TartUncompressedDiskSize: fmt.Sprint(totalUncompressed),
			ociimage.TartUploadTime:           "2026-04-26T00:00:00Z",
		},
	}
	return manifest, blobs
}

// newOCITartBlobRegistry serves both the manifest and arbitrary blob bodies.
// Used by TestPullDispatch_TartFullPull for the full fetch+decompress+verify
// path; the dispatch-only registry returns 404 on blobs.
func newOCITartBlobRegistry(t *testing.T, manifest ociimage.Manifest, blobs map[string][]byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/me/dev-vm/manifests/v1":
			w.Header().Set("Content-Type", ociimage.MediaTypeImageManifest)
			w.Header().Set("Docker-Content-Digest", "sha256:dispatch-test")
			if err := json.NewEncoder(w).Encode(manifest); err != nil {
				t.Errorf("encode manifest: %v", err)
			}
		case strings.HasPrefix(r.URL.Path, "/v2/me/dev-vm/blobs/"):
			digest := strings.TrimPrefix(r.URL.Path, "/v2/me/dev-vm/blobs/")
			data, ok := blobs[digest]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(data)
		default:
			t.Errorf("unexpected request path %q", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
