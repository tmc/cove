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

func TestParseBearerChallenge(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   bearerChallenge
		ok     bool
	}{
		{
			name:   "simple",
			header: `Bearer realm="https://auth.example/token",service="registry.example.com",scope="repository:team/vm:pull"`,
			want: bearerChallenge{
				Realm:   "https://auth.example/token",
				Service: "registry.example.com",
				Scope:   "repository:team/vm:pull",
			},
			ok: true,
		},
		{
			name:   "mixed case",
			header: `bEaReR realm="https://auth.example/token"`,
			want:   bearerChallenge{Realm: "https://auth.example/token"},
			ok:     true,
		},
		{
			name:   "skips other challenge",
			header: `Basic realm="registry", Bearer realm="https://auth.example/token",service="svc"`,
			want:   bearerChallenge{Realm: "https://auth.example/token", Service: "svc"},
			ok:     true,
		},
		{
			name:   "quoted comma",
			header: `Bearer realm="https://auth.example/token?a=1,b=2",service="svc"`,
			want:   bearerChallenge{Realm: "https://auth.example/token?a=1,b=2", Service: "svc"},
			ok:     true,
		},
		{
			name:   "escaped quote",
			header: `Bearer realm="https://auth.example/t\"oken"`,
			want:   bearerChallenge{Realm: `https://auth.example/t"oken`},
			ok:     true,
		},
		{name: "missing realm", header: `Bearer service="svc"`},
		{name: "non bearer", header: `Basic realm="registry"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseBearerChallenge(tt.header)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if got != tt.want {
				t.Fatalf("challenge = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParseBearerChallenges(t *testing.T) {
	got, ok := parseBearerChallenges([]string{
		`Basic realm="registry"`,
		`Bearer realm="https://auth.example/token",service="svc"`,
	})
	if !ok {
		t.Fatal("ok = false, want true")
	}
	if got.Realm != "https://auth.example/token" || got.Service != "svc" {
		t.Fatalf("challenge = %#v", got)
	}
}

func TestRegistryClientFetchManifestBearerChallenge(t *testing.T) {
	ref := Reference{Registry: "registry.example.com", Repository: "team/vm", Tag: "latest"}
	var srv *httptest.Server
	manifestRequests := 0
	tokenRequests := 0
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			tokenRequests++
			if r.Header.Get("Authorization") != "" {
				t.Fatalf("token Authorization = %q, want empty", r.Header.Get("Authorization"))
			}
			if got, want := r.URL.Query().Get("service"), "registry.example.com"; got != want {
				t.Fatalf("service = %q, want %q", got, want)
			}
			if got, want := r.URL.Query().Get("scope"), "repository:team/vm:pull"; got != want {
				t.Fatalf("scope = %q, want %q", got, want)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "pull-token"})
		case "/v2/team/vm/manifests/latest":
			manifestRequests++
			if manifestRequests == 1 {
				w.Header().Set("WWW-Authenticate", `Bearer realm="`+srv.URL+`/token",service="registry.example.com",scope="repository:team/vm:pull"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.Header.Get("Authorization") != "Bearer pull-token" {
				t.Fatalf("Authorization = %q, want bearer token", r.Header.Get("Authorization"))
			}
			if r.Header.Get("Accept") != MediaTypeImageManifest {
				t.Fatalf("Accept = %q", r.Header.Get("Accept"))
			}
			_ = json.NewEncoder(w).Encode(Manifest{SchemaVersion: 2})
		default:
			t.Fatalf("%s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()

	if _, _, err := (RegistryClient{BaseURL: srv.URL, TokenCache: NewRegistryTokenCache()}).FetchManifest(context.Background(), ref); err != nil {
		t.Fatalf("FetchManifest(): %v", err)
	}
	if manifestRequests != 2 || tokenRequests != 1 {
		t.Fatalf("requests = manifest %d token %d, want 2 and 1", manifestRequests, tokenRequests)
	}
}

func TestRegistryClientBearerChallengeWithBasicAuthorization(t *testing.T) {
	ref := Reference{Registry: "registry.example.com", Repository: "team/vm", Tag: "latest"}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			if r.Header.Get("Authorization") != "Basic abc" {
				t.Fatalf("token Authorization = %q, want Basic abc", r.Header.Get("Authorization"))
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "pull-token"})
		case "/v2/team/vm/blobs/sha256:abcd":
			switch r.Header.Get("Authorization") {
			case "Basic abc":
				w.Header().Set("WWW-Authenticate", `Bearer realm="`+srv.URL+`/token",service="registry.example.com",scope="repository:team/vm:pull"`)
				w.WriteHeader(http.StatusUnauthorized)
			case "Bearer pull-token":
				_, _ = w.Write([]byte("blob-data"))
			default:
				t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
			}
		default:
			t.Fatalf("%s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()

	body, err := (RegistryClient{
		BaseURL:       srv.URL,
		Authorization: "Basic abc",
		TokenCache:    NewRegistryTokenCache(),
	}).FetchBlob(context.Background(), ref, "sha256:abcd")
	if err != nil {
		t.Fatalf("FetchBlob(): %v", err)
	}
	body.Close()
}

func TestRegistryClientPushManifestBearerChallengeReplaysBody(t *testing.T) {
	ref := Reference{Registry: "registry.example.com", Repository: "team/vm", Tag: "latest"}
	manifest := Manifest{SchemaVersion: 2, MediaType: MediaTypeImageManifest}
	var srv *httptest.Server
	puts := 0
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "push-token"})
		case "/v2/team/vm/manifests/latest":
			puts++
			if puts == 1 {
				w.Header().Set("WWW-Authenticate", `Bearer realm="`+srv.URL+`/token",service="registry.example.com",scope="repository:team/vm:push"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.Header.Get("Authorization") != "Bearer push-token" {
				t.Fatalf("Authorization = %q, want bearer token", r.Header.Get("Authorization"))
			}
			if r.Header.Get("Content-Type") != MediaTypeImageManifest {
				t.Fatalf("Content-Type = %q, want %q", r.Header.Get("Content-Type"), MediaTypeImageManifest)
			}
			var got Manifest
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if got.SchemaVersion != manifest.SchemaVersion {
				t.Fatalf("manifest = %#v, want %#v", got, manifest)
			}
			w.WriteHeader(http.StatusCreated)
		default:
			t.Fatalf("%s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()

	if _, err := (RegistryClient{BaseURL: srv.URL, TokenCache: NewRegistryTokenCache()}).PushManifest(context.Background(), ref, manifest); err != nil {
		t.Fatalf("PushManifest(): %v", err)
	}
	if puts != 2 {
		t.Fatalf("puts = %d, want 2", puts)
	}
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

func TestRegistryClientUploadBlobReusesBearerTokenForCommit(t *testing.T) {
	ref := Reference{Registry: "registry.example.com", Repository: "team/vm", Tag: "latest"}
	data := []byte("blob-data")
	desc := Descriptor{Size: int64(len(data)), Digest: testDigest(data)}
	var srv *httptest.Server
	starts := 0
	commits := 0
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/token":
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "push-token"})
		case r.Method == http.MethodPost && r.URL.Path == "/v2/team/vm/blobs/uploads/":
			starts++
			if starts == 1 {
				w.Header().Set("WWW-Authenticate", `Bearer realm="`+srv.URL+`/token",service="registry.example.com",scope="repository:team/vm:pull,push"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			if r.Header.Get("Authorization") != "Bearer push-token" {
				t.Fatalf("start Authorization = %q, want bearer token", r.Header.Get("Authorization"))
			}
			w.Header().Set("Location", "/v2/team/vm/blobs/uploads/upload-id")
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodPut && r.URL.Path == "/v2/team/vm/blobs/uploads/upload-id":
			commits++
			if r.Header.Get("Authorization") != "Bearer push-token" {
				t.Fatalf("commit Authorization = %q, want bearer token", r.Header.Get("Authorization"))
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

	err := (RegistryClient{BaseURL: srv.URL, TokenCache: NewRegistryTokenCache()}).UploadBlob(context.Background(), ref, desc, strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("UploadBlob(): %v", err)
	}
	if starts != 2 || commits != 1 {
		t.Fatalf("requests = starts %d commits %d, want 2 and 1", starts, commits)
	}
}

func TestRegistryClientUploadBlobDoesNotRetryNonReplayableCommit(t *testing.T) {
	ref := Reference{Registry: "registry.example.com", Repository: "team/vm", Tag: "latest"}
	data := []byte("blob-data")
	desc := Descriptor{Size: int64(len(data)), Digest: testDigest(data)}
	var srv *httptest.Server
	commits := 0
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/token":
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "push-token"})
		case r.Method == http.MethodPost && r.URL.Path == "/v2/team/vm/blobs/uploads/":
			w.Header().Set("Location", "/v2/team/vm/blobs/uploads/upload-id")
			w.WriteHeader(http.StatusAccepted)
		case r.Method == http.MethodPut && r.URL.Path == "/v2/team/vm/blobs/uploads/upload-id":
			commits++
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+srv.URL+`/token",service="registry.example.com",scope="repository:team/vm:push"`)
			w.WriteHeader(http.StatusUnauthorized)
		default:
			t.Fatalf("%s %s", r.Method, r.URL.String())
		}
	}))
	defer srv.Close()

	err := (RegistryClient{BaseURL: srv.URL, TokenCache: NewRegistryTokenCache()}).UploadBlob(context.Background(), ref, desc, &oneShotReader{data: data})
	if err == nil || !strings.Contains(err.Error(), "cannot retry request body") {
		t.Fatalf("UploadBlob() error = %v, want non-replayable body error", err)
	}
	if commits != 1 {
		t.Fatalf("commits = %d, want 1", commits)
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

type oneShotReader struct {
	data []byte
}

func (r *oneShotReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}
