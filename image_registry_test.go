package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote/auth"
)

func TestIsRegistryHostClassification(t *testing.T) {
	tests := []struct {
		host string
		want bool
	}{
		{"localhost", true},
		{"localhost:5000", true},
		{"ghcr.io", true},
		{"registry.example.com", true},
		{"host:5000", true},
		{"docker", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isRegistryHost(tt.host); got != tt.want {
			t.Errorf("isRegistryHost(%q) = %v, want %v", tt.host, got, tt.want)
		}
	}
}

func TestIsPublicRegistryClassification(t *testing.T) {
	tests := []struct {
		registry string
		want     bool
	}{
		{"docker.io", true},
		{"  Docker.IO  ", true},
		{"index.docker.io", true},
		{"registry-1.docker.io", true},
		{"ghcr.io", true},
		{"quay.io", true},
		{"registry.gitlab.com", true},
		{"localhost", false},
		{"localhost:5000", false},
		{"registry.example.com", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isPublicRegistry(tt.registry); got != tt.want {
			t.Errorf("isPublicRegistry(%q) = %v, want %v", tt.registry, got, tt.want)
		}
	}
}

func TestIsRegistryReferenceClassification(t *testing.T) {
	tests := []struct {
		ref  string
		want bool
	}{
		{"ghcr.io/me/img:v1", true},
		{"localhost:5000/img:v1", true},
		{"ghcr.io/me/img", false},
		{"docker/img:v1", false},
		{"", false},
		{"::::", false},
	}
	for _, tt := range tests {
		if got := isRegistryReference(tt.ref); got != tt.want {
			t.Errorf("isRegistryReference(%q) = %v, want %v", tt.ref, got, tt.want)
		}
	}
}

func TestPushImageToTargetPacksCoveArtifact(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ref := buildSampleImage(t, "src", "registry:v1")
	store := memory.New()

	root, err := PushImageToTarget(context.Background(), ref, store, "registry-v1")
	if err != nil {
		t.Fatalf("PushImageToTarget: %v", err)
	}
	resolved, err := store.Resolve(context.Background(), "registry-v1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Digest != root.Digest {
		t.Fatalf("resolved digest = %s, want %s", resolved.Digest, root.Digest)
	}

	data, err := content.FetchAll(context.Background(), store, root)
	if err != nil {
		t.Fatalf("FetchAll manifest: %v", err)
	}
	var manifest ocispec.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("Unmarshal manifest: %v", err)
	}
	if manifest.ArtifactType != coveImageArtifactType {
		t.Fatalf("artifactType = %q, want %q", manifest.ArtifactType, coveImageArtifactType)
	}
	if manifest.Config.MediaType != coveImageConfigType {
		t.Fatalf("config mediaType = %q, want %q", manifest.Config.MediaType, coveImageConfigType)
	}
	if len(manifest.Layers) != len(imageDataFiles) {
		t.Fatalf("layer count = %d, want %d", len(manifest.Layers), len(imageDataFiles))
	}
	if got := manifest.Layers[0].MediaType; got != coveImageDiskType {
		t.Fatalf("disk mediaType = %q, want %q", got, coveImageDiskType)
	}
	if got := manifest.Layers[0].Annotations[ociTitleAnnotation]; got != "disk.img.gz" {
		t.Fatalf("disk title = %q, want disk.img.gz", got)
	}
	for _, layer := range manifest.Layers {
		if layer.Annotations[ociTitleAnnotation] == "" {
			t.Fatalf("layer %s missing title annotation", layer.Digest)
		}
	}
}

func TestParseRegistryImageRef(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr string
	}{
		{name: "tag", in: "registry.example.com/acme/vm:v1"},
		{name: "digest", in: "registry.example.com/acme/vm@sha256:9b7af890cc7bc4c6a2f07cf367602ba4d548090457a81a5de54eed228dbca5f6"},
		{name: "missing registry", in: "acme/vm:v1", wantErr: "registry ref"},
		{name: "missing reference", in: "registry.example.com/acme/vm", wantErr: "tag or digest"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseRegistryImageRef(tt.in)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("parseRegistryImageRef: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("parseRegistryImageRef succeeded; want error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestEnsurePrivateRegistryPush(t *testing.T) {
	tests := []struct {
		name      string
		registry  string
		allow     string
		wantError bool
	}{
		{name: "private", registry: "registry.example.com"},
		{name: "ghcr blocked", registry: "ghcr.io", wantError: true},
		{name: "docker blocked", registry: "docker.io", wantError: true},
		{name: "escape hatch", registry: "ghcr.io", allow: "1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("COVE_ALLOW_PUBLIC_PUSH", tt.allow)
			err := ensurePrivateRegistryPush(registryImageRef{Reference: registry.Reference{Registry: tt.registry}})
			if tt.wantError {
				if err == nil {
					t.Fatal("ensurePrivateRegistryPush succeeded; want error")
				}
				if !strings.Contains(err.Error(), "refusing public registry") {
					t.Fatalf("error = %q, want public registry refusal", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ensurePrivateRegistryPush: %v", err)
			}
		})
	}
}

func TestRunImagePushRefusesPublicRegistry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("COVE_ALLOW_PUBLIC_PUSH", "")
	ref := buildSampleImage(t, "src", "public:v1")

	err := runImagePush([]string{ref.String(), "ghcr.io/acme/vm:v1"})
	if err == nil {
		t.Fatal("runImagePush succeeded; want public registry refusal")
	}
	if !strings.Contains(err.Error(), "refusing public registry ghcr.io") {
		t.Fatalf("error = %q, want ghcr public registry refusal", err)
	}
}

func TestNewImageRepositoryUsesDockerCredentialHelper(t *testing.T) {
	clearRegistryAuthEnv(t)
	binDir := t.TempDir()
	helper := filepath.Join(binDir, "docker-credential-testhelper")
	writeHelper(t, helper)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	writeDockerConfig(t, `{"credHelpers":{"ghcr.io":"testhelper"}}`)

	repo, err := newImageRepository(registryImageRef{Reference: registry.Reference{Registry: "ghcr.io", Repository: "acme/vm", Reference: "v1"}})
	if err != nil {
		t.Fatalf("newImageRepository: %v", err)
	}
	client, ok := repo.Client.(*auth.Client)
	if !ok {
		t.Fatalf("repo.Client type = %T, want *auth.Client", repo.Client)
	}
	cred, err := client.Credential(context.Background(), "ghcr.io")
	if err != nil {
		t.Fatalf("client.Credential: %v", err)
	}
	if cred.Username != "helper-user" || cred.Password != "helper-secret" {
		t.Fatalf("credential = %q/%q, want helper-user/helper-secret", cred.Username, cred.Password)
	}
}

func TestNewImageRepositoryReportsDockerConfigError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{"), 0o644); err != nil {
		t.Fatalf("WriteFile(config.json): %v", err)
	}
	t.Setenv("DOCKER_CONFIG", dir)

	_, err := newImageRepository(registryImageRef{Reference: registry.Reference{Registry: "ghcr.io", Repository: "acme/vm", Reference: "v1"}})
	if err == nil {
		t.Fatal("newImageRepository succeeded; want docker config error")
	}
	if !strings.Contains(err.Error(), "registry auth: load docker credentials") {
		t.Fatalf("error = %q, want docker auth load failure", err)
	}
}

func TestPullImageFromTargetRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	src := buildSampleImage(t, "src", "pullsrc:v1")
	store := memory.New()
	if _, err := PushImageToTarget(context.Background(), src, store, "pull-v1"); err != nil {
		t.Fatalf("PushImageToTarget: %v", err)
	}

	originals := map[string][]byte{}
	for _, name := range append([]string{"manifest.json"}, imageDataFiles...) {
		b, err := os.ReadFile(filepath.Join(src.Path(), name))
		if err != nil {
			t.Fatalf("read original %s: %v", name, err)
		}
		originals[name] = b
	}
	if err := os.RemoveAll(src.Path()); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}

	got, _, err := PullImageFromTarget(context.Background(), store, "pull-v1", "", false)
	if err != nil {
		t.Fatalf("PullImageFromTarget: %v", err)
	}
	if got.String() != src.String() {
		t.Fatalf("pulled ref = %s, want %s", got, src)
	}
	for name, want := range originals {
		gotBytes, err := os.ReadFile(filepath.Join(got.Path(), name))
		if err != nil {
			t.Fatalf("read pulled %s: %v", name, err)
		}
		if string(gotBytes) != string(want) {
			t.Fatalf("%s differs after pull", name)
		}
	}
}

func TestPullImageFromTargetRenameAndDuplicate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	src := buildSampleImage(t, "src", "pullrename:v1")
	store := memory.New()
	if _, err := PushImageToTarget(context.Background(), src, store, "pull-rename"); err != nil {
		t.Fatalf("PushImageToTarget: %v", err)
	}

	renamed, _, err := PullImageFromTarget(context.Background(), store, "pull-rename", "renamed:v2", false)
	if err != nil {
		t.Fatalf("PullImageFromTarget rename: %v", err)
	}
	if renamed.String() != "renamed:v2" {
		t.Fatalf("renamed ref = %s, want renamed:v2", renamed)
	}
	if _, _, err := PullImageFromTarget(context.Background(), store, "pull-rename", "renamed:v2", false); err == nil {
		t.Fatal("PullImageFromTarget duplicate succeeded; want error")
	}
	if _, _, err := PullImageFromTarget(context.Background(), store, "pull-rename", "renamed:v2", true); err != nil {
		t.Fatalf("PullImageFromTarget force: %v", err)
	}
}
