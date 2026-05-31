package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tmc/cove/internal/buildscratch"
	"github.com/tmc/cove/internal/ociimage"
	"github.com/tmc/cove/internal/store"
	"github.com/tmc/cove/proto/controlpb"
)

func TestBuildRegistryCacheExportImportRoundTrip(t *testing.T) {
	root := t.TempDir()
	srcStore := store.New(filepath.Join(root, "src-store"))
	parent := filepath.Join(root, "parent.img")
	child := filepath.Join(root, "child.img")
	if err := os.WriteFile(parent, []byte("parent disk\n"), 0644); err != nil {
		t.Fatal(err)
	}
	want := []byte("cached disk\n")
	if err := os.WriteFile(child, want, 0644); err != nil {
		t.Fatal(err)
	}
	step := testBuildPlanStep("cached", digestBytes([]byte("cache-key")))
	layer := storeBuildRegistryCacheTestLayer(t, srcStore, step, parent, child)
	reg := newBuildCacheTestRegistry(t)
	defer reg.Close()

	opts := buildOptions{CacheTo: []string{"ghcr.io/me/cache:build"}, RegistryBaseURL: reg.URL()}
	if err := exportBuildRegistryCaches(context.Background(), buildPlan{Steps: []buildPlanStep{step}}, opts, srcStore); err != nil {
		t.Fatalf("exportBuildRegistryCaches(): %v", err)
	}
	if _, ok := reg.Manifest("build"); !ok {
		t.Fatal("cache manifest was not pushed")
	}

	dstStore := store.New(filepath.Join(root, "dst-store"))
	if err := importBuildRegistryCaches(context.Background(), buildOptions{CacheFrom: opts.CacheTo, RegistryBaseURL: reg.URL()}, dstStore); err != nil {
		t.Fatalf("importBuildRegistryCaches(): %v", err)
	}
	entry, err := loadBuildCacheEntry(dstStore, step.Key)
	if err != nil {
		t.Fatalf("loadBuildCacheEntry(): %v", err)
	}
	if entry.LayerDigest != layer.Digest {
		t.Fatalf("layer digest = %s, want %s", entry.LayerDigest, layer.Digest)
	}
	importedLayer, err := loadBuildLayerManifest(dstStore, layer.Digest)
	if err != nil {
		t.Fatalf("loadBuildLayerManifest(): %v", err)
	}
	restored := filepath.Join(root, "restored.img")
	if err := ApplyStoredDiskDelta(context.Background(), dstStore, parent, restored, importedLayer); err != nil {
		t.Fatalf("ApplyStoredDiskDelta(): %v", err)
	}
	got, err := os.ReadFile(restored)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("restored = %q, want %q", got, want)
	}
}

func TestHandleBuildImportsCacheBeforeDryRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	script := filepath.Join(home, "hello.vzscript")
	if err := os.WriteFile(script, []byte("exec echo hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	base := "ghcr.io/acme/base@sha256:" + strings.Repeat("a", 64)
	srcStore := store.New(filepath.Join(home, "src-store"))
	opts := buildOptions{Base: base, Scripts: []string{script}, Compact: "targeted", StoreDir: srcStore.Dir}
	plan, err := buildDryPlanWithStore(context.Background(), "test-image", opts, nil, srcStore)
	if err != nil {
		t.Fatalf("buildDryPlanWithStore(): %v", err)
	}
	parent := filepath.Join(home, "parent.img")
	child := filepath.Join(home, "child.img")
	if err := os.WriteFile(parent, []byte("parent\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, []byte("child\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cacheRef := "ghcr.io/me/cache:build"
	oldImporter := buildRegistryCacheImporter
	defer func() { buildRegistryCacheImporter = oldImporter }()
	buildRegistryCacheImporter = func(ctx context.Context, opts buildOptions, s store.Store) error {
		if !reflect.DeepEqual(opts.CacheFrom, []string{cacheRef}) {
			t.Fatalf("cache-from = %#v, want %q", opts.CacheFrom, cacheRef)
		}
		storeBuildRegistryCacheTestLayer(t, s, plan.Steps[0], parent, child)
		return nil
	}

	out, err := captureStdoutResult(t, func() error {
		return handleBuild([]string{
			"test-image",
			"--base", base,
			"--script", script,
			"--store-dir", filepath.Join(home, "dst-store"),
			"--cache-from", cacheRef,
			"--dry-run",
		})
	})
	if err != nil {
		t.Fatalf("handleBuild(): %v", err)
	}
	if !strings.Contains(out, "cache: hit") {
		t.Fatalf("output missing imported cache hit:\n%s", out)
	}
}

func TestHandleBuildCallsCacheExporter(t *testing.T) {
	restoreControl := stubBuildControlSender(t, func(call *int, sock string, req *controlpb.ControlRequest, timeout time.Duration, cmdType string) (*controlpb.ControlResponse, error) {
		return &controlpb.ControlResponse{Success: true}, nil
	})
	defer restoreControl()
	oldStart := defaultBuildGuestStart
	oldCompact := defaultBuildCompact
	oldExporter := buildRegistryCacheExporter
	defer func() {
		defaultBuildGuestStart = oldStart
		defaultBuildCompact = oldCompact
		buildRegistryCacheExporter = oldExporter
	}()
	defaultBuildGuestStart = func(context.Context, buildscratch.Scratch) (buildGuestCleanup, error) {
		return func(context.Context) error { return nil }, nil
	}
	defaultBuildCompact = func(context.Context, buildscratch.Scratch, string) error { return nil }
	cacheRef := "ghcr.io/me/cache:build"
	exported := false
	buildRegistryCacheExporter = func(ctx context.Context, plan buildPlan, opts buildOptions, s store.Store) error {
		exported = true
		if !reflect.DeepEqual(opts.CacheTo, []string{cacheRef}) {
			t.Fatalf("cache-to = %#v, want %q", opts.CacheTo, cacheRef)
		}
		if len(plan.Steps) != 1 {
			t.Fatalf("export plan steps = %d, want 1", len(plan.Steps))
		}
		return nil
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	parentDir := filepath.Join(home, "parent")
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"disk.img":   "base image\n",
		"aux.img":    "aux",
		"hw.model":   "hw",
		"machine.id": "machine",
	} {
		if err := os.WriteFile(filepath.Join(parentDir, name), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	script := filepath.Join(home, "hello.vzscript")
	if err := os.WriteFile(script, []byte("echo hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdoutResult(t, func() error {
		return handleBuild([]string{
			"test-image",
			"--base", parentDir,
			"--script", script,
			"--store-dir", filepath.Join(home, "store"),
			"--cache-to", cacheRef,
			"--compact", "fast",
		})
	})
	if err != nil {
		t.Fatalf("handleBuild(): %v", err)
	}
	if !strings.Contains(out, "Build complete") {
		t.Fatalf("output missing build result:\n%s", out)
	}
	if !exported {
		t.Fatal("cache exporter was not called")
	}
}

func TestHandleBuildChecksScriptsBeforeCacheImport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	oldImporter := buildRegistryCacheImporter
	defer func() { buildRegistryCacheImporter = oldImporter }()
	buildRegistryCacheImporter = func(context.Context, buildOptions, store.Store) error {
		t.Fatal("cache importer was called before script validation")
		return nil
	}
	missing := filepath.Join(home, "missing.vzscript")
	err := handleBuild([]string{
		"test-image",
		"--base", "ghcr.io/acme/base@sha256:" + strings.Repeat("a", 64),
		"--script", missing,
		"--cache-from", "ghcr.io/me/cache:build",
		"--dry-run",
	})
	if err == nil {
		t.Fatal("handleBuild() error = nil, want missing script error")
	}
	if !strings.Contains(err.Error(), "missing.vzscript") {
		t.Fatalf("handleBuild() error = %v, want missing script", err)
	}
}

func storeBuildRegistryCacheTestLayer(t *testing.T, s store.Store, step buildPlanStep, parent, child string) buildLayerManifest {
	t.Helper()
	delta, err := DiffDisks(parent, child)
	if err != nil {
		t.Fatal(err)
	}
	layer, err := StoreDiskDelta(s, delta)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveBuildLayerManifest(s, layer); err != nil {
		t.Fatal(err)
	}
	if err := saveBuildCacheEntry(s, testCacheEntryForStep(step, layer.Digest)); err != nil {
		t.Fatal(err)
	}
	return layer
}

type buildCacheTestRegistry struct {
	t         *testing.T
	srv       *httptest.Server
	mu        sync.Mutex
	blobs     map[string][]byte
	manifests map[string]ociimage.Manifest
}

func newBuildCacheTestRegistry(t *testing.T) *buildCacheTestRegistry {
	t.Helper()
	reg := &buildCacheTestRegistry{
		t:         t,
		blobs:     map[string][]byte{},
		manifests: map[string]ociimage.Manifest{},
	}
	reg.srv = httptest.NewServer(http.HandlerFunc(reg.serveHTTP))
	return reg
}

func (r *buildCacheTestRegistry) URL() string {
	return r.srv.URL
}

func (r *buildCacheTestRegistry) Close() {
	r.srv.Close()
}

func (r *buildCacheTestRegistry) Manifest(tag string) (ociimage.Manifest, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	manifest, ok := r.manifests[tag]
	return manifest, ok
}

func (r *buildCacheTestRegistry) serveHTTP(w http.ResponseWriter, req *http.Request) {
	const blobPrefix = "/v2/me/cache/blobs/"
	const uploadPrefix = "/v2/me/cache/blobs/uploads/"
	switch {
	case req.Method == http.MethodHead && strings.HasPrefix(req.URL.Path, blobPrefix):
		digest := strings.TrimPrefix(req.URL.Path, blobPrefix)
		r.mu.Lock()
		_, ok := r.blobs[digest]
		r.mu.Unlock()
		if ok {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	case req.Method == http.MethodPost && req.URL.Path == uploadPrefix:
		w.Header().Set("Location", uploadPrefix+"upload-id")
		w.WriteHeader(http.StatusAccepted)
	case req.Method == http.MethodPut && req.URL.Path == uploadPrefix+"upload-id":
		digest := req.URL.Query().Get("digest")
		data, err := io.ReadAll(req.Body)
		if err != nil {
			r.t.Fatalf("ReadAll() error = %v", err)
		}
		if got := digestBytes(data); got != digest {
			r.t.Fatalf("uploaded digest = %q, want %q", got, digest)
		}
		r.mu.Lock()
		r.blobs[digest] = data
		r.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	case req.Method == http.MethodPut && strings.HasPrefix(req.URL.Path, "/v2/me/cache/manifests/"):
		tag := strings.TrimPrefix(req.URL.Path, "/v2/me/cache/manifests/")
		var manifest ociimage.Manifest
		if err := json.NewDecoder(req.Body).Decode(&manifest); err != nil {
			r.t.Fatalf("Decode() error = %v", err)
		}
		r.mu.Lock()
		r.manifests[tag] = manifest
		r.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	case req.Method == http.MethodGet && strings.HasPrefix(req.URL.Path, "/v2/me/cache/manifests/"):
		tag := strings.TrimPrefix(req.URL.Path, "/v2/me/cache/manifests/")
		r.mu.Lock()
		manifest, ok := r.manifests[tag]
		r.mu.Unlock()
		if !ok {
			http.NotFound(w, req)
			return
		}
		data, err := json.Marshal(manifest)
		if err != nil {
			r.t.Fatalf("Marshal() error = %v", err)
		}
		w.Header().Set("Docker-Content-Digest", digestData(data))
		_, _ = w.Write(data)
	case req.Method == http.MethodGet && strings.HasPrefix(req.URL.Path, blobPrefix):
		digest := strings.TrimPrefix(req.URL.Path, blobPrefix)
		r.mu.Lock()
		data, ok := r.blobs[digest]
		r.mu.Unlock()
		if !ok {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write(data)
	default:
		r.t.Fatalf("%s %s", req.Method, req.URL.String())
	}
}
