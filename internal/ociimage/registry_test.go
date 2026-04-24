package ociimage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRegistryClientFetchManifest(t *testing.T) {
	ref := Reference{Registry: "registry.example.com", Repository: "team/vm", Tag: "latest"}
	want := Manifest{
		SchemaVersion: 2,
		MediaType:     MediaTypeImageManifest,
		Annotations: map[string]string{
			CoveUncompressedDiskSize: "0",
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/v2/team/vm/manifests/latest" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Accept") != MediaTypeImageManifest {
			t.Fatalf("Accept = %q", r.Header.Get("Accept"))
		}
		w.Header().Set("Docker-Content-Digest", "sha256:manifest")
		if err := json.NewEncoder(w).Encode(want); err != nil {
			t.Fatalf("Encode() error = %v", err)
		}
	}))
	defer srv.Close()

	got, digest, err := (RegistryClient{BaseURL: srv.URL}).FetchManifest(context.Background(), ref)
	if err != nil {
		t.Fatalf("FetchManifest(): %v", err)
	}
	if digest != "sha256:manifest" {
		t.Fatalf("digest = %q, want sha256:manifest", digest)
	}
	if got.SchemaVersion != want.SchemaVersion || got.MediaType != want.MediaType {
		t.Fatalf("manifest = %#v, want %#v", got, want)
	}
}

func TestRegistryClientFetchManifestByDigest(t *testing.T) {
	ref := Reference{Registry: "registry.example.com", Repository: "team/vm", Digest: "sha256:abcd"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/team/vm/manifests/sha256:abcd" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(Manifest{SchemaVersion: 2})
	}))
	defer srv.Close()

	if _, _, err := (RegistryClient{BaseURL: srv.URL}).FetchManifest(context.Background(), ref); err != nil {
		t.Fatalf("FetchManifest(): %v", err)
	}
}

func TestRegistryClientFetchManifestRejectsErrors(t *testing.T) {
	ref := Reference{Registry: "registry.example.com", Repository: "team/vm"}
	if _, _, err := (RegistryClient{}).FetchManifest(context.Background(), ref); err == nil {
		t.Fatal("FetchManifest() error = nil, want missing tag or digest")
	}

	ref.Tag = "latest"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	if _, _, err := (RegistryClient{BaseURL: srv.URL}).FetchManifest(context.Background(), ref); err == nil {
		t.Fatal("FetchManifest() error = nil, want registry error")
	}
}

func TestRegistryClientBlobExists(t *testing.T) {
	ref := Reference{Registry: "registry.example.com", Repository: "team/vm", Tag: "latest"}
	tests := []struct {
		name   string
		status int
		want   bool
	}{
		{name: "exists", status: http.StatusOK, want: true},
		{name: "missing", status: http.StatusNotFound, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				if r.Method != http.MethodHead {
					t.Fatalf("method = %s, want HEAD", r.Method)
				}
				if r.Header.Get("Authorization") != "Bearer token" {
					t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
				}
				w.WriteHeader(tt.status)
			}))
			defer srv.Close()

			got, err := (RegistryClient{BaseURL: srv.URL, Token: "token"}).BlobExists(context.Background(), ref, "sha256:abcd")
			if err != nil {
				t.Fatalf("BlobExists(): %v", err)
			}
			if got != tt.want {
				t.Fatalf("BlobExists() = %v, want %v", got, tt.want)
			}
			if gotPath != "/v2/team/vm/blobs/sha256:abcd" {
				t.Fatalf("path = %q", gotPath)
			}
		})
	}
}

func TestRegistryClientBlobExistsRejectsRegistryError(t *testing.T) {
	ref := Reference{Registry: "registry.example.com", Repository: "team/vm", Tag: "latest"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := (RegistryClient{BaseURL: srv.URL}).BlobExists(context.Background(), ref, "sha256:abcd")
	if err == nil {
		t.Fatal("BlobExists() error = nil, want registry error")
	}
}

func TestRegistryClientURLValidation(t *testing.T) {
	ref := Reference{Registry: "registry.example.com", Repository: "team/vm", Tag: "latest"}
	if _, err := (RegistryClient{BaseURL: "://bad"}).BlobExists(context.Background(), ref, "sha256:abcd"); err == nil {
		t.Fatal("BlobExists() error = nil, want bad base URL")
	}
	if _, err := (RegistryClient{}).BlobExists(context.Background(), Reference{}, "sha256:abcd"); err == nil {
		t.Fatal("BlobExists() error = nil, want incomplete reference")
	}
	if _, err := (RegistryClient{}).BlobExists(context.Background(), ref, ""); err == nil {
		t.Fatal("BlobExists() error = nil, want empty digest")
	}
}
