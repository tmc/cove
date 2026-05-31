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
	"sync/atomic"
	"testing"
)

func TestMaterializeBuildRegistryBasePullsCoveManifest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	diskData := []byte("registry base disk")
	manifest, blobs := pullCompressedTestManifest(t, diskData)
	_, manifestDigest := pullTestManifestData(t, manifest)
	srv := pullTestRegistry(t, manifest, blobs)
	defer srv.Close()

	dir, cleanup, err := materializeBuildRegistryBase(context.Background(), "ghcr.io/me/dev-vm:v1", buildOptions{RegistryBaseURL: srv.URL})
	if err != nil {
		t.Fatalf("materializeBuildRegistryBase(): %v", err)
	}
	if cleanup == nil {
		t.Fatal("cleanup = nil")
	}
	got, err := os.ReadFile(filepath.Join(dir, "disk.img"))
	if err != nil {
		t.Fatalf("ReadFile(disk.img): %v", err)
	}
	if !bytes.Equal(got, diskData) {
		t.Fatalf("disk = %q, want %q", got, diskData)
	}
	for _, name := range []string{"aux.img", "hw.model", "machine.id", "disk.provenance"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
	}
	metaData, err := os.ReadFile(filepath.Join(dir, "build-registry-base.json"))
	if err != nil {
		t.Fatalf("ReadFile(build-registry-base.json): %v", err)
	}
	var meta buildRegistryBaseMeta
	if err := json.Unmarshal(metaData, &meta); err != nil {
		t.Fatalf("Unmarshal(build-registry-base.json): %v", err)
	}
	if meta.Format != "cove" || meta.DiskFormat != "raw" || meta.ManifestDigest != manifestDigest {
		t.Fatalf("registry base meta = %+v, want cove raw %s", meta, manifestDigest)
	}
	if err := cleanup(); err != nil {
		t.Fatalf("cleanup(): %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("cached dir stat after cleanup: %v", err)
	}
}

func TestMaterializeBuildRegistryBaseReusesCachedBase(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	diskData := []byte("registry base disk")
	manifest, blobs := pullCompressedTestManifest(t, diskData)
	manifestData, manifestDigest := pullTestManifestData(t, manifest)
	var failBlobs atomic.Bool
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
			if failBlobs.Load() {
				http.Error(w, "blob fetch disabled", http.StatusInternalServerError)
				return
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
	defer srv.Close()

	opts := buildOptions{RegistryBaseURL: srv.URL}
	first, cleanup, err := materializeBuildRegistryBase(context.Background(), "ghcr.io/me/dev-vm:v1", opts)
	if err != nil {
		t.Fatalf("first materializeBuildRegistryBase(): %v", err)
	}
	if err := cleanup(); err != nil {
		t.Fatalf("cleanup(): %v", err)
	}
	if got := blobGets.Load(); got == 0 {
		t.Fatal("first materialization made no blob requests")
	}
	if err := os.RemoveAll(filepath.Join(home, ".vz", "store", "blobs")); err != nil {
		t.Fatalf("remove store blobs: %v", err)
	}
	failBlobs.Store(true)

	before := blobGets.Load()
	second, cleanup, err := materializeBuildRegistryBase(context.Background(), "ghcr.io/me/dev-vm:v1", opts)
	if err != nil {
		t.Fatalf("second materializeBuildRegistryBase(): %v", err)
	}
	if err := cleanup(); err != nil {
		t.Fatalf("second cleanup(): %v", err)
	}
	if second != first {
		t.Fatalf("cached dir = %q, want %q", second, first)
	}
	if got := blobGets.Load(); got != before {
		t.Fatalf("blob requests after cache hit = %d, want %d", got, before)
	}
}

func TestMaterializeBuildRegistryBaseDigestRefUsesOfflineCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	digest := "sha256:" + strings.Repeat("a", 64)
	dir, err := buildRegistryBaseCacheDir(buildOptions{}, digest)
	if err != nil {
		t.Fatalf("buildRegistryBaseCacheDir(): %v", err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	for name, data := range map[string]string{
		"disk.img":        "cached disk",
		"aux.img":         "aux",
		"disk.provenance": digest + "\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0644); err != nil {
			t.Fatalf("WriteFile(%s): %v", name, err)
		}
	}

	got, cleanup, err := materializeBuildRegistryBase(context.Background(), fmt.Sprintf("ghcr.io/me/dev-vm@%s", digest), buildOptions{
		RegistryBaseURL: "http://127.0.0.1:1",
	})
	if err != nil {
		t.Fatalf("materializeBuildRegistryBase(): %v", err)
	}
	if err := cleanup(); err != nil {
		t.Fatalf("cleanup(): %v", err)
	}
	if got != dir {
		t.Fatalf("cached dir = %q, want %q", got, dir)
	}
}
