package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/ociimage"
)

func TestEnsurePushBaseBlobSameRepoExists(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Fatalf("method = %s, want HEAD", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/blobs/sha256:abc") {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ref := ociimage.Reference{Registry: "registry.example.com", Repository: "team/vm", Tag: "latest"}
	client := ociimage.RegistryClient{BaseURL: srv.URL}
	desc := ociimage.Descriptor{Digest: "sha256:abc"}

	got, err := ensurePushBaseBlob(context.Background(), client, ref, ref, desc)
	if err != nil {
		t.Fatalf("ensurePushBaseBlob: %v", err)
	}
	if !got {
		t.Fatal("got false, want true (blob exists)")
	}
}

func TestEnsurePushBaseBlobSameRepoMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	ref := ociimage.Reference{Registry: "registry.example.com", Repository: "team/vm", Tag: "latest"}
	client := ociimage.RegistryClient{BaseURL: srv.URL}
	desc := ociimage.Descriptor{Digest: "sha256:def"}

	got, err := ensurePushBaseBlob(context.Background(), client, ref, ref, desc)
	if err != nil {
		t.Fatalf("ensurePushBaseBlob: %v", err)
	}
	if got {
		t.Fatal("got true, want false (blob missing)")
	}
}

func TestEnsurePushBaseBlobCrossRepoMounts(t *testing.T) {
	target := ociimage.Reference{Registry: "registry.example.com", Repository: "team/vm", Tag: "latest"}
	source := ociimage.Reference{Registry: "registry.example.com", Repository: "team/base", Tag: "v1"}
	desc := ociimage.Descriptor{Digest: "sha256:abcd"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST (mount)", r.Method)
		}
		if got := r.URL.Query().Get("mount"); got != desc.Digest {
			t.Fatalf("mount = %q, want %q", got, desc.Digest)
		}
		if got := r.URL.Query().Get("from"); got != source.Repository {
			t.Fatalf("from = %q, want %q", got, source.Repository)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	client := ociimage.RegistryClient{BaseURL: srv.URL}
	got, err := ensurePushBaseBlob(context.Background(), client, target, source, desc)
	if err != nil {
		t.Fatalf("ensurePushBaseBlob: %v", err)
	}
	if !got {
		t.Fatal("got false, want true (mount succeeded)")
	}
}

func TestEnsurePushBaseBlobSameRepoServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ref := ociimage.Reference{Registry: "registry.example.com", Repository: "team/vm", Tag: "latest"}
	client := ociimage.RegistryClient{BaseURL: srv.URL}
	desc := ociimage.Descriptor{Digest: "sha256:abc"}

	_, err := ensurePushBaseBlob(context.Background(), client, ref, ref, desc)
	if err == nil {
		t.Fatal("expected error on 500 from BlobExists")
	}
}
