package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/registry"
)

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
