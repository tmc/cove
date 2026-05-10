package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/vz-macos/internal/ociimage"
)

func newTartSidecarServer(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/blobs/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func tartSidecarPlan(t *testing.T, base string) (ociimage.RegistryClient, *pullPlan) {
	t.Helper()
	u := strings.TrimPrefix(base, "http://")
	client := ociimage.RegistryClient{BaseURL: base}
	plan := &pullPlan{
		Ref:   ociimage.Reference{Registry: u, Repository: "acme/macos", Tag: "latest"},
		VMDir: t.TempDir(),
	}
	return client, plan
}

func TestTartPullSidecarRejectsEmptyDigest(t *testing.T) {
	client, plan := tartSidecarPlan(t, "http://example.invalid")
	err := tartPullSidecar(context.Background(), client, plan, ociimage.Descriptor{Size: 4}, "config.json")
	if err == nil || !strings.Contains(err.Error(), "missing digest") {
		t.Fatalf("err = %v, want missing digest", err)
	}
}

func TestTartPullSidecarHappyPath(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	srv := newTartSidecarServer(t, body)
	client, plan := tartSidecarPlan(t, srv.URL)

	desc := ociimage.Descriptor{Digest: digest, Size: int64(len(body))}
	if err := tartPullSidecar(context.Background(), client, plan, desc, "config.json"); err != nil {
		t.Fatalf("tartPullSidecar: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(plan.VMDir, "config.json"))
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	if string(got) != string(body) {
		t.Fatalf("body = %q, want %q", got, body)
	}
	if _, err := os.Stat(filepath.Join(plan.VMDir, "config.json.tmp")); !os.IsNotExist(err) {
		t.Fatalf("tmp not cleaned: %v", err)
	}
}

func TestTartPullSidecarRejectsSizeMismatch(t *testing.T) {
	body := []byte("0123456789")
	sum := sha256.Sum256(body)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	srv := newTartSidecarServer(t, body)
	client, plan := tartSidecarPlan(t, srv.URL)

	desc := ociimage.Descriptor{Digest: digest, Size: 5}
	err := tartPullSidecar(context.Background(), client, plan, desc, "config.json")
	if err == nil || !strings.Contains(err.Error(), "size") {
		t.Fatalf("err = %v, want size mismatch", err)
	}
	if _, statErr := os.Stat(filepath.Join(plan.VMDir, "config.json.tmp")); !os.IsNotExist(statErr) {
		t.Errorf("tmp not cleaned: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(plan.VMDir, "config.json")); !os.IsNotExist(statErr) {
		t.Errorf("dst should not exist after size mismatch: %v", statErr)
	}
}

func TestTartPullSidecarRejectsDigestMismatch(t *testing.T) {
	body := []byte("0123456789")
	srv := newTartSidecarServer(t, body)
	client, plan := tartSidecarPlan(t, srv.URL)

	desc := ociimage.Descriptor{
		Digest: "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Size:   int64(len(body)),
	}
	err := tartPullSidecar(context.Background(), client, plan, desc, "config.json")
	if err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("err = %v, want digest mismatch", err)
	}
	if _, statErr := os.Stat(filepath.Join(plan.VMDir, "config.json")); !os.IsNotExist(statErr) {
		t.Errorf("dst should not exist after digest mismatch: %v", statErr)
	}
}

func TestTartPullSidecarFetchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	client, plan := tartSidecarPlan(t, srv.URL)
	desc := ociimage.Descriptor{Digest: "sha256:deadbeef", Size: 1}
	err := tartPullSidecar(context.Background(), client, plan, desc, "config.json")
	if err == nil || !strings.Contains(err.Error(), "fetch") {
		t.Fatalf("err = %v, want fetch error", err)
	}
}
