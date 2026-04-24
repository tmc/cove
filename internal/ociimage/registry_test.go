package ociimage

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestRegistryClientFetchBlob(t *testing.T) {
	ref := Reference{Registry: "registry.example.com", Repository: "team/vm", Tag: "latest"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/v2/team/vm/blobs/sha256:abcd" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte("blob-data"))
	}))
	defer srv.Close()

	body, err := (RegistryClient{BaseURL: srv.URL, Token: "token"}).FetchBlob(context.Background(), ref, "sha256:abcd")
	if err != nil {
		t.Fatalf("FetchBlob(): %v", err)
	}
	defer body.Close()

	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if string(data) != "blob-data" {
		t.Fatalf("body = %q, want blob-data", string(data))
	}
}

func TestRegistryClientAuthorizationHeader(t *testing.T) {
	ref := Reference{Registry: "registry.example.com", Repository: "team/vm", Tag: "latest"}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Basic abc" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte("blob-data"))
	}))
	defer srv.Close()

	body, err := (RegistryClient{
		BaseURL:       srv.URL,
		Token:         "token",
		Authorization: "Basic abc",
	}).FetchBlob(context.Background(), ref, "sha256:abcd")
	if err != nil {
		t.Fatalf("FetchBlob(): %v", err)
	}
	body.Close()
}

func TestRegistryClientFetchBlobRejectsErrors(t *testing.T) {
	ref := Reference{Registry: "registry.example.com", Repository: "team/vm", Tag: "latest"}
	if _, err := (RegistryClient{}).FetchBlob(context.Background(), ref, ""); err == nil {
		t.Fatal("FetchBlob() error = nil, want empty digest")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	if _, err := (RegistryClient{BaseURL: srv.URL}).FetchBlob(context.Background(), ref, "sha256:abcd"); err == nil {
		t.Fatal("FetchBlob() error = nil, want registry error")
	}
}

func TestRegistryClientUploadBlob(t *testing.T) {
	ref := Reference{Registry: "registry.example.com", Repository: "team/vm", Tag: "latest"}
	data := []byte("blob-data")
	desc := Descriptor{Size: int64(len(data)), Digest: testDigest(data)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v2/team/vm/blobs/uploads/":
			w.Header().Set("Location", "/v2/team/vm/blobs/uploads/upload-id")
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodPut && r.URL.Path == "/v2/team/vm/blobs/uploads/upload-id":
			if got := r.URL.Query().Get("digest"); got != desc.Digest {
				t.Fatalf("digest query = %q, want %q", got, desc.Digest)
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("ReadAll() error = %v", err)
			}
			if string(body) != string(data) {
				t.Fatalf("body = %q, want %q", string(body), string(data))
			}
			w.WriteHeader(http.StatusCreated)
		default:
			t.Fatalf("%s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()

	err := (RegistryClient{BaseURL: srv.URL, Token: "token"}).UploadBlob(context.Background(), ref, desc, strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("UploadBlob(): %v", err)
	}
}

func TestRegistryClientUploadBlobRejectsErrors(t *testing.T) {
	ref := Reference{Registry: "registry.example.com", Repository: "team/vm", Tag: "latest"}
	if err := (RegistryClient{}).UploadBlob(context.Background(), ref, Descriptor{}, strings.NewReader("")); err == nil {
		t.Fatal("UploadBlob() error = nil, want missing digest")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	desc := Descriptor{Size: 1, Digest: testDigest([]byte{1})}
	if err := (RegistryClient{BaseURL: srv.URL}).UploadBlob(context.Background(), ref, desc, strings.NewReader("x")); err == nil {
		t.Fatal("UploadBlob() error = nil, want registry error")
	}
}

func TestRegistryClientPushManifest(t *testing.T) {
	ref := Reference{Registry: "registry.example.com", Repository: "team/vm", Tag: "latest"}
	manifest := Manifest{SchemaVersion: 2, MediaType: MediaTypeImageManifest}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("method = %s, want PUT", r.Method)
		}
		if r.URL.Path != "/v2/team/vm/manifests/latest" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != MediaTypeImageManifest {
			t.Fatalf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		var got Manifest
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		if got.SchemaVersion != manifest.SchemaVersion {
			t.Fatalf("manifest = %#v", got)
		}
		w.Header().Set("Docker-Content-Digest", "sha256:manifest")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	digest, err := (RegistryClient{BaseURL: srv.URL}).PushManifest(context.Background(), ref, manifest)
	if err != nil {
		t.Fatalf("PushManifest(): %v", err)
	}
	if digest != "sha256:manifest" {
		t.Fatalf("digest = %q, want sha256:manifest", digest)
	}
}

func TestRegistryClientPushManifestRejectsErrors(t *testing.T) {
	ref := Reference{Registry: "registry.example.com", Repository: "team/vm"}
	if _, err := (RegistryClient{}).PushManifest(context.Background(), ref, Manifest{}); err == nil {
		t.Fatal("PushManifest() error = nil, want missing tag or digest")
	}

	ref.Tag = "latest"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	if _, err := (RegistryClient{BaseURL: srv.URL}).PushManifest(context.Background(), ref, Manifest{}); err == nil {
		t.Fatal("PushManifest() error = nil, want registry error")
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
