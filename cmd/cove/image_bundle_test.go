package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/ociimage"
)

func TestVerifyImageBundleValidatesCompleteBundle(t *testing.T) {
	dir := writeTestManifestBundle(t)

	report := VerifyImageBundle(dir)
	if report.Verdict != imageVerifyPass {
		t.Fatalf("VerifyImageBundle verdict = %s, want pass\n%+v", report.Verdict, report.Checks)
	}
	if report.Ref != "ghcr.io/me/dev-vm:v1" || report.SelectedPlatform != "linux/arm64" || report.Format != "cove" || report.DiskFormat != "raw" || report.ChildCount != 2 {
		t.Fatalf("bundle report = %+v, want ref/linux cove raw two children", report)
	}
	for _, want := range []string{"index digest", "selected digest", "child metadata", "selected summary"} {
		if !imageBundleReportHasCheck(report, want, imageVerifyPass) {
			t.Fatalf("bundle report checks missing pass %q: %+v", want, report.Checks)
		}
	}
}

func TestRunImageBundleVerifyRejectsTamperedChildJSON(t *testing.T) {
	dir := writeTestManifestBundle(t)
	if err := os.WriteFile(filepath.Join(dir, "manifests", manifestBundleDigestName(testManifestBundleLinuxDigest(t, dir))+".json"), []byte("{}\n"), 0644); err != nil {
		t.Fatalf("tamper child: %v", err)
	}

	var stdout strings.Builder
	err := runImageBundleVerify(commandEnv{Stdout: &stdout, Stderr: &strings.Builder{}}, []string{"-json", dir})
	if err == nil || !strings.Contains(err.Error(), "image bundle verify") {
		t.Fatalf("runImageBundleVerify error = %v, want verify failure", err)
	}
	var report imageBundleVerifyReport
	if err := json.Unmarshal([]byte(stdout.String()), &report); err != nil {
		t.Fatalf("parse verify JSON: %v\n%s", err, stdout.String())
	}
	if report.Verdict != imageVerifyFail {
		t.Fatalf("verify JSON verdict = %s, want fail\n%s", report.Verdict, stdout.String())
	}
	if !imageBundleReportHasCheck(report, "child "+report.SelectedDigest+" digest", imageVerifyFail) {
		t.Fatalf("verify JSON checks missing child digest failure: %+v", report.Checks)
	}
}

func TestRunImageBundleHelp(t *testing.T) {
	var stdout strings.Builder
	if err := runImageBundle(commandEnv{Stdout: &stdout, Stderr: &strings.Builder{}}, []string{"-h"}); err != nil {
		t.Fatalf("runImageBundle -h: %v", err)
	}
	if !strings.Contains(stdout.String(), "Usage: cove image bundle") || !strings.Contains(stdout.String(), "verify") {
		t.Fatalf("bundle help = %q, want usage and verify", stdout.String())
	}
}

func writeTestManifestBundle(t *testing.T) string {
	t.Helper()
	darwinManifest, _, _ := pullCompressedChunkedTestManifest(t, []byte("darwin"), 3)
	linuxManifest, _, _ := pullCompressedChunkedTestManifest(t, []byte("linux-child"), 5)
	darwinData, darwinDigest := pullTestManifestData(t, darwinManifest)
	linuxData, linuxDigest := pullTestManifestData(t, linuxManifest)
	index := ociimage.Index{
		SchemaVersion: 2,
		MediaType:     ociimage.MediaTypeImageIndex,
		Manifests: []ociimage.IndexDescriptor{
			{
				Descriptor: ociimage.Descriptor{MediaType: ociimage.MediaTypeImageManifest, Size: int64(len(darwinData)), Digest: darwinDigest},
				Platform:   &ociimage.Platform{OS: "darwin", Architecture: "arm64"},
			},
			{
				Descriptor: ociimage.Descriptor{MediaType: ociimage.MediaTypeImageManifest, Size: int64(len(linuxData)), Digest: linuxDigest},
				Platform:   &ociimage.Platform{OS: "linux", Architecture: "arm64"},
			},
		},
	}
	indexData := remoteInspectIndexData(t, index)
	dir := filepath.Join(t.TempDir(), "bundle")
	children := []manifestBundleChild{
		{Digest: darwinDigest, Data: darwinData},
		{Digest: linuxDigest, Data: linuxData},
	}
	summary := manifestBundleSummary{
		SchemaVersion:      1,
		Source:             "test",
		Ref:                "ghcr.io/me/dev-vm:v1",
		IndexPath:          "index.json",
		IndexDigest:        digestData(indexData),
		IndexFileDigest:    digestData(indexData),
		IndexMediaType:     ociimage.MediaTypeImageIndex,
		SelectedPath:       "selected.json",
		ManifestDigest:     linuxDigest,
		SelectedFileDigest: linuxDigest,
		DigestRef:          "ghcr.io/me/dev-vm@" + linuxDigest,
		ResolvedFromIndex:  true,
		SelectedDigest:     linuxDigest,
		SelectedPlatform:   "linux/arm64",
		Kind:               "vm-oci",
		Format:             "cove",
		PullPlan:           "cove chunked pull",
		DiskSize:           int64(len("linux-child")),
		DiskFormat:         "raw",
		ChildCount:         2,
		Children: []manifestBundleChildSummary{
			{
				Digest:        darwinDigest,
				Path:          manifestBundleChildPath(darwinDigest),
				FileDigest:    darwinDigest,
				MediaType:     ociimage.MediaTypeImageManifest,
				Size:          int64(len(darwinData)),
				Platform:      "darwin/arm64",
				Kind:          "vm-oci",
				Format:        "cove",
				PullPlan:      "cove chunked pull",
				DiskSize:      int64(len("darwin")),
				DiskFormat:    "raw",
				ChunkCount:    2,
				MetadataBlobs: 3,
			},
			{
				Digest:        linuxDigest,
				Path:          manifestBundleChildPath(linuxDigest),
				FileDigest:    linuxDigest,
				MediaType:     ociimage.MediaTypeImageManifest,
				Size:          int64(len(linuxData)),
				Platform:      "linux/arm64",
				Selected:      true,
				Kind:          "vm-oci",
				Format:        "cove",
				PullPlan:      "cove chunked pull",
				DiskSize:      int64(len("linux-child")),
				DiskFormat:    "raw",
				ChunkCount:    3,
				MetadataBlobs: 3,
			},
		},
	}
	if err := writeManifestBundle(dir, indexData, linuxData, children, &summary); err != nil {
		t.Fatalf("writeManifestBundle: %v", err)
	}
	return dir
}

func testManifestBundleLinuxDigest(t *testing.T, dir string) string {
	t.Helper()
	report := VerifyImageBundle(dir)
	if report.SelectedDigest == "" {
		t.Fatalf("test bundle missing selected digest: %+v", report)
	}
	return report.SelectedDigest
}

func imageBundleReportHasCheck(report imageBundleVerifyReport, name string, status imageVerifyStatus) bool {
	for _, check := range report.Checks {
		if check.Name == name && check.Status == status {
			return true
		}
	}
	return false
}
