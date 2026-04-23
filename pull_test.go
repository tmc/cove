package main

import (
	"encoding/json"
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

func writePullTestManifest(t *testing.T) string {
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
