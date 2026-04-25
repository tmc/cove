package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
