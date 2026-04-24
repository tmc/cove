package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/vz-macos/internal/ociimage"
)

func TestBuildPullPlanDryRunManifest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	manifestPath := writePullTestManifest(t)

	plan, err := buildPullPlan("ghcr.io/me/dev-vm:v1", pullOptions{
		As:           "local-dev",
		DryRun:       true,
		ManifestPath: manifestPath,
	})
	if err != nil {
		t.Fatalf("buildPullPlan(): %v", err)
	}
	if plan.VMName != "local-dev" {
		t.Fatalf("VMName = %q, want local-dev", plan.VMName)
	}
	if plan.Ref.String() != "ghcr.io/me/dev-vm:v1" {
		t.Fatalf("Ref = %q", plan.Ref.String())
	}
	if got, want := len(plan.Manifest.Chunks), 1; got != want {
		t.Fatalf("chunks = %d, want %d", got, want)
	}
	if plan.Manifest.Annotations.UncompressedDiskSize != 3 {
		t.Fatalf("disk size = %d, want 3", plan.Manifest.Annotations.UncompressedDiskSize)
	}
}

func TestBuildPullPlanDryRunFetchesManifest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	manifest := pullTestManifest(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/v2/me/dev-vm/manifests/v1" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.Header().Set("Docker-Content-Digest", "sha256:manifest")
		if err := json.NewEncoder(w).Encode(manifest); err != nil {
			t.Fatalf("Encode() error = %v", err)
		}
	}))
	defer srv.Close()

	plan, err := buildPullPlan("ghcr.io/me/dev-vm:v1", pullOptions{
		DryRun:          true,
		RegistryBaseURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("buildPullPlan(): %v", err)
	}
	if plan.ManifestDigest != "sha256:manifest" {
		t.Fatalf("ManifestDigest = %q, want sha256:manifest", plan.ManifestDigest)
	}
	if got, want := len(plan.Manifest.Chunks), 1; got != want {
		t.Fatalf("chunks = %d, want %d", got, want)
	}
}

func TestHandlePullDryRunOutput(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	manifestPath := writePullTestManifest(t)

	out, err := captureStdoutResult(t, func() error {
		return handlePull([]string{
			"--dry-run",
			"--manifest", manifestPath,
			"--as", "local-dev",
			"ghcr.io/me/dev-vm:v1",
		})
	})
	if err != nil {
		t.Fatalf("handlePull(): %v", err)
	}
	for _, want := range []string{
		"Pull dry run",
		"ref: ghcr.io/me/dev-vm:v1",
		"vm: local-dev",
		"chunks: 1",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output %q missing %q", out, want)
		}
	}
}

func TestHandlePullRequiresDryRun(t *testing.T) {
	err := handlePull([]string{"ghcr.io/me/dev-vm:v1"})
	if err == nil || !strings.Contains(err.Error(), "use --dry-run") {
		t.Fatalf("handlePull() error = %v, want dry-run guidance", err)
	}
}

func TestParsePullArgs(t *testing.T) {
	opts, pos, err := parsePullArgs([]string{
		"--as", "local-dev",
		"--dry-run",
		"--manifest", "manifest.json",
		"ghcr.io/me/dev-vm:v1",
	}, ioDiscard{})
	if err != nil {
		t.Fatalf("parsePullArgs(): %v", err)
	}
	if !opts.DryRun || opts.As != "local-dev" || opts.ManifestPath != "manifest.json" {
		t.Fatalf("opts = %#v", opts)
	}
	if strings.Join(pos, ",") != "ghcr.io/me/dev-vm:v1" {
		t.Fatalf("pos = %#v", pos)
	}
}

func TestBuildPullPlanRejectsInvalidRef(t *testing.T) {
	_, err := buildPullPlan("me/dev-vm", pullOptions{DryRun: true})
	if err == nil || !strings.Contains(err.Error(), "invalid ref") {
		t.Fatalf("buildPullPlan() error = %v, want invalid ref", err)
	}
}

func TestBuildPullPlanRejectsIncompleteTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	vmPath := filepath.Join(home, ".vz", "vms", "dev-vm")
	if err := os.MkdirAll(vmPath, 0755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(vmPath, "disk.img.partial"), []byte("partial"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	_, err := buildPullPlan("ghcr.io/me/dev-vm:v1", pullOptions{DryRun: true})
	if err == nil || !strings.Contains(err.Error(), "pull was interrupted") {
		t.Fatalf("buildPullPlan() error = %v, want incomplete disk", err)
	}
}

func TestPullRegistryToken(t *testing.T) {
	ref := ociimage.Reference{Registry: "ghcr.io"}
	if got := pullRegistryToken(ref, pullOptions{RegistryToken: "explicit"}); got != "explicit" {
		t.Fatalf("token = %q, want explicit", got)
	}
	t.Setenv("COVE_REGISTRY_TOKEN", "cove-token")
	if got := pullRegistryToken(ref, pullOptions{}); got != "cove-token" {
		t.Fatalf("token = %q, want cove-token", got)
	}
	t.Setenv("COVE_REGISTRY_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "github-token")
	if got := pullRegistryToken(ref, pullOptions{}); got != "github-token" {
		t.Fatalf("token = %q, want github-token", got)
	}
	if got := pullRegistryToken(ociimage.Reference{Registry: "registry.example.com"}, pullOptions{}); got != "" {
		t.Fatalf("token = %q, want empty", got)
	}
}

func writePullTestManifest(t *testing.T) string {
	t.Helper()

	manifest := pullTestManifest(t)
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func pullTestManifest(t *testing.T) ociimage.Manifest {
	t.Helper()

	manifest, _, err := ociimage.BuildManifest(ociimage.ManifestOptions{
		UploadTime: "2026-04-23T00:00:00Z",
		DiskSize:   3,
		Chunks: []ociimage.Chunk{
			{Index: 0, Offset: 0, Size: 3, Digest: pushTestDigest([]byte{1, 2, 3})},
		},
	})
	if err != nil {
		t.Fatalf("BuildManifest(): %v", err)
	}
	return manifest
}
