package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/vz-macos/internal/ociimage"
)

func TestPutOpenVerified(t *testing.T) {
	s := New(t.TempDir())
	data := []byte("compressed chunk")
	digest := testDigest(data)
	if err := s.Put(digest, int64(len(data)), bytes.NewReader(data)); err != nil {
		t.Fatalf("Put(): %v", err)
	}
	f, err := s.OpenVerified(digest, int64(len(data)))
	if err != nil {
		t.Fatalf("OpenVerified(): %v", err)
	}
	defer f.Close()
	got := make([]byte, len(data))
	if _, err := f.Read(got); err != nil {
		t.Fatalf("Read(): %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("blob = %q, want %q", got, data)
	}
}

func TestGCRespectsSharedPullLockAndGrace(t *testing.T) {
	s := New(t.TempDir())
	oldData := []byte("old")
	oldDigest := testDigest(oldData)
	if err := s.Put(oldDigest, int64(len(oldData)), bytes.NewReader(oldData)); err != nil {
		t.Fatalf("Put(old): %v", err)
	}
	oldPath, err := s.BlobPath(oldDigest)
	if err != nil {
		t.Fatalf("BlobPath(old): %v", err)
	}
	oldTime := time.Now().Add(-2 * GCGrace)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes(old): %v", err)
	}

	youngData := []byte("young")
	youngDigest := testDigest(youngData)
	if err := s.Put(youngDigest, int64(len(youngData)), bytes.NewReader(youngData)); err != nil {
		t.Fatalf("Put(young): %v", err)
	}

	unlock, err := s.LockShared()
	if err != nil {
		t.Fatalf("LockShared(): %v", err)
	}
	done := make(chan GCResult, 1)
	errc := make(chan error, 1)
	go func() {
		res, err := s.GCWithOptions(nil, GCOptions{Grace: GCGrace})
		if err != nil {
			errc <- err
			return
		}
		done <- res
	}()
	select {
	case <-done:
		t.Fatal("GC completed while shared pull lock was held")
	case err := <-errc:
		t.Fatalf("GC error while waiting: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := unlock(); err != nil {
		t.Fatalf("unlock: %v", err)
	}

	var res GCResult
	select {
	case res = <-done:
	case err := <-errc:
		t.Fatalf("GC(): %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("GC did not finish after shared lock release")
	}
	if res.Deleted != 1 || res.KeptYoung != 1 {
		t.Fatalf("GC result = %+v, want one deleted and one young", res)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old blob stat error = %v, want not exist", err)
	}
	youngPath, err := s.BlobPath(youngDigest)
	if err != nil {
		t.Fatalf("BlobPath(young): %v", err)
	}
	if _, err := os.Stat(youngPath); err != nil {
		t.Fatalf("young blob stat: %v", err)
	}
}

func TestGCDryRunDoesNotDelete(t *testing.T) {
	s := New(t.TempDir())
	data := []byte("old")
	digest := testDigest(data)
	if err := s.Put(digest, int64(len(data)), bytes.NewReader(data)); err != nil {
		t.Fatalf("Put(): %v", err)
	}
	path, err := s.BlobPath(digest)
	if err != nil {
		t.Fatalf("BlobPath(): %v", err)
	}
	oldTime := time.Now().Add(-2 * GCGrace)
	if err := os.Chtimes(path, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes(): %v", err)
	}

	res, err := s.GCWithOptions(nil, GCOptions{Grace: GCGrace, DryRun: true})
	if err != nil {
		t.Fatalf("GCWithOptions(dry-run): %v", err)
	}
	if res.Deleted != 1 || res.Reclaimed != int64(len(data)) {
		t.Fatalf("GC result = %+v, want one candidate", res)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("dry-run deleted blob: %v", err)
	}
}

func TestReachableFromVMsUsesStoredManifest(t *testing.T) {
	s := New(t.TempDir())
	blobDigest := testDigest([]byte("blob"))
	manifest := ociimage.Manifest{
		SchemaVersion: 2,
		MediaType:     ociimage.MediaTypeImageManifest,
		Layers: []ociimage.Descriptor{{
			MediaType: ociimage.MediaTypeLayer,
			Size:      4,
			Digest:    blobDigest,
		}},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("Marshal(): %v", err)
	}
	manifestDigest := testDigest(data)
	if err := s.StoreManifest(manifestDigest, data); err != nil {
		t.Fatalf("StoreManifest(): %v", err)
	}
	vms := t.TempDir()
	vmDir := filepath.Join(vms, "vm")
	if err := os.MkdirAll(vmDir, 0755); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	if err := os.WriteFile(filepath.Join(vmDir, "disk.provenance"), []byte(manifestDigest+"\n"), 0644); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}
	reachable, err := s.ReachableFromVMs(vms)
	if err != nil {
		t.Fatalf("ReachableFromVMs(): %v", err)
	}
	if !reachable[blobDigest] {
		t.Fatalf("reachable[%s] = false", blobDigest)
	}
}

func TestStoreManifestRejectsInvalidDigest(t *testing.T) {
	s := New(t.TempDir())
	if err := s.StoreManifest("sha256:not-a-real-digest", []byte("{}")); err == nil {
		t.Fatal("StoreManifest() error = nil, want invalid digest error")
	}
}

func TestReachableFromBuildCache(t *testing.T) {
	s := New(t.TempDir())
	want := testDigest([]byte("layer"))
	path := filepath.Join(s.Dir, "build-cache", "keys", "key.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	data := []byte(`{"layer_digest":"` + want + `","chunks":["sha256:not-a-real-digest"]}`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}
	reachable, err := s.ReachableFromBuildCache()
	if err != nil {
		t.Fatalf("ReachableFromBuildCache(): %v", err)
	}
	if !reachable[want] {
		t.Fatalf("reachable[%s] = false", want)
	}
	if reachable["sha256:not-a-real-digest"] {
		t.Fatal("invalid digest marked reachable")
	}
}

func TestReachableFromBuildCacheFollowsParentManifest(t *testing.T) {
	s := New(t.TempDir())
	configDigest := testDigest([]byte("config"))
	layerDigest := testDigest([]byte("layer"))
	manifest := ociimage.Manifest{
		SchemaVersion: 2,
		MediaType:     ociimage.MediaTypeImageManifest,
		Config: ociimage.Descriptor{
			MediaType: ociimage.MediaTypeImageConfig,
			Size:      6,
			Digest:    configDigest,
		},
		Layers: []ociimage.Descriptor{{
			MediaType: ociimage.MediaTypeLayer,
			Size:      5,
			Digest:    layerDigest,
		}},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("Marshal(): %v", err)
	}
	manifestDigest := testDigest(data)
	if err := s.StoreManifest(manifestDigest, data); err != nil {
		t.Fatalf("StoreManifest(): %v", err)
	}
	path := filepath.Join(s.Dir, "build-cache", "keys", "key.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	data = []byte(`{"parent_digest":"` + manifestDigest + `","layer_digest":"` + testDigest([]byte("build layer")) + `"}`)
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}
	reachable, err := s.ReachableFromBuildCache()
	if err != nil {
		t.Fatalf("ReachableFromBuildCache(): %v", err)
	}
	for _, digest := range []string{manifestDigest, configDigest, layerDigest} {
		if !reachable[digest] {
			t.Fatalf("reachable[%s] = false", digest)
		}
	}
}

func TestReachableFromBuildCacheFollowsLayerManifest(t *testing.T) {
	s := New(t.TempDir())
	blockDigest := testDigest([]byte("block"))
	layerDigest := testDigest([]byte("build layer manifest"))
	keyPath := filepath.Join(s.Dir, "build-cache", "keys", "key.json")
	if err := os.MkdirAll(filepath.Dir(keyPath), 0755); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	if err := os.WriteFile(keyPath, []byte(`{"layer_digest":"`+layerDigest+`"}`), 0644); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}
	layerPath := filepath.Join(s.Dir, "build-cache", "layers", strings.TrimPrefix(layerDigest, "sha256:")+".json")
	if err := os.MkdirAll(filepath.Dir(layerPath), 0755); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	if err := os.WriteFile(layerPath, []byte(`{"digest":"`+layerDigest+`","blocks":[{"digest":"`+blockDigest+`"}]}`), 0644); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}
	reachable, err := s.ReachableFromBuildCache()
	if err != nil {
		t.Fatalf("ReachableFromBuildCache(): %v", err)
	}
	if !reachable[layerDigest] || !reachable[blockDigest] {
		t.Fatalf("reachable layer = %v block = %v, want true", reachable[layerDigest], reachable[blockDigest])
	}
}

func TestGCKeepsBuildCacheReachableBlob(t *testing.T) {
	s := New(t.TempDir())
	keptData := []byte("kept")
	keptDigest := testDigest(keptData)
	if err := s.Put(keptDigest, int64(len(keptData)), bytes.NewReader(keptData)); err != nil {
		t.Fatalf("Put(kept): %v", err)
	}
	deletedData := []byte("deleted")
	deletedDigest := testDigest(deletedData)
	if err := s.Put(deletedDigest, int64(len(deletedData)), bytes.NewReader(deletedData)); err != nil {
		t.Fatalf("Put(deleted): %v", err)
	}
	old := time.Now().Add(-2 * GCGrace)
	for _, digest := range []string{keptDigest, deletedDigest} {
		path, err := s.BlobPath(digest)
		if err != nil {
			t.Fatalf("BlobPath(): %v", err)
		}
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatalf("Chtimes(): %v", err)
		}
	}
	cachePath := filepath.Join(s.Dir, "build-cache", "layers", "layer.json")
	if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err != nil {
		t.Fatalf("MkdirAll(): %v", err)
	}
	if err := os.WriteFile(cachePath, []byte(`{"blob":"`+keptDigest+`"}`), 0644); err != nil {
		t.Fatalf("WriteFile(): %v", err)
	}
	reachable, err := s.ReachableFromBuildCache()
	if err != nil {
		t.Fatalf("ReachableFromBuildCache(): %v", err)
	}
	res, err := s.GCWithOptions(reachable, GCOptions{Grace: GCGrace})
	if err != nil {
		t.Fatalf("GC(): %v", err)
	}
	if res.Deleted != 1 {
		t.Fatalf("Deleted = %d, want 1", res.Deleted)
	}
	if res.KeptReachable != 1 {
		t.Fatalf("KeptReachable = %d, want 1", res.KeptReachable)
	}
	keptPath, err := s.BlobPath(keptDigest)
	if err != nil {
		t.Fatalf("BlobPath(kept): %v", err)
	}
	if _, err := os.Stat(keptPath); err != nil {
		t.Fatalf("kept blob stat: %v", err)
	}
	deletedPath, err := s.BlobPath(deletedDigest)
	if err != nil {
		t.Fatalf("BlobPath(deleted): %v", err)
	}
	if _, err := os.Stat(deletedPath); !os.IsNotExist(err) {
		t.Fatalf("deleted blob stat error = %v, want not exist", err)
	}
}

func TestEnsureRefetchesCorruptBlob(t *testing.T) {
	s := New(t.TempDir())
	good := []byte("good")
	digest := testDigest(good)
	if err := s.Put(digest, int64(len(good)), bytes.NewReader(good)); err != nil {
		t.Fatalf("Put(): %v", err)
	}
	path, err := s.BlobPath(digest)
	if err != nil {
		t.Fatalf("BlobPath(): %v", err)
	}
	if err := os.WriteFile(path, []byte("bad!"), 0644); err != nil {
		t.Fatalf("corrupt blob: %v", err)
	}
	called := false
	err = s.Ensure(context.Background(), digest, int64(len(good)), func(context.Context) (io.ReadCloser, error) {
		called = true
		return io.NopCloser(bytes.NewReader(good)), nil
	})
	if err != nil {
		t.Fatalf("Ensure(): %v", err)
	}
	if !called {
		t.Fatal("Ensure did not refetch corrupt blob")
	}
}

func testDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
