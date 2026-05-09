package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tmc/vz-macos/internal/ociimage"
)

func TestPullNameFromReference(t *testing.T) {
	tests := []struct {
		name string
		ref  ociimage.Reference
		want string
	}{
		{"single segment", ociimage.Reference{Repository: "macos"}, "macos"},
		{"two segments", ociimage.Reference{Repository: "acme/macos"}, "macos"},
		{"deep path", ociimage.Reference{Repository: "acme/team/macos-15"}, "macos-15"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pullNameFromReference(tt.ref); got != tt.want {
				t.Errorf("pullNameFromReference(%q) = %q, want %q", tt.ref.Repository, got, tt.want)
			}
		})
	}
}

func TestPrintPullResult(t *testing.T) {
	plan := &pullPlan{
		Ref:    ociimage.Reference{Registry: "ghcr.io", Repository: "acme/macos", Tag: "v1"},
		VMName: "dev",
		VMDir:  "/tmp/dev",
	}
	var buf bytes.Buffer
	printPullResult(&buf, plan)
	out := buf.String()
	for _, want := range []string{"Pull complete", "ghcr.io/acme/macos:v1", "vm: dev", "target: /tmp/dev"} {
		if !strings.Contains(out, want) {
			t.Errorf("printPullResult output missing %q\n--- got ---\n%s", want, out)
		}
	}
}

func TestPrintPullDryRunFormats(t *testing.T) {
	ref := ociimage.Reference{Registry: "ghcr.io", Repository: "acme/macos", Tag: "v1"}
	tests := []struct {
		name string
		plan *pullPlan
		want []string
		deny []string
	}{
		{
			name: "lume",
			plan: &pullPlan{
				Ref:            ref,
				VMName:         "dev",
				VMDir:          "/tmp/dev",
				ManifestDigest: "sha256:abc",
				Manifest: ociimage.ParsedManifest{
					Format: ociimage.FormatLume,
					Lume: ociimage.LumeManifest{
						DiskParts:   []ociimage.LumeLayer{{}, {}},
						NvramLayer:  &ociimage.Descriptor{Size: 1024},
						ConfigLayer: &ociimage.Descriptor{Size: 256},
					},
				},
			},
			want: []string{"format: lume", "disk parts: 2", "nvram.bin:", "config.json:", "manifest digest: sha256:abc"},
		},
		{
			name: "tart",
			plan: &pullPlan{
				Ref:    ref,
				VMName: "dev",
				VMDir:  "/tmp/dev",
				Manifest: ociimage.ParsedManifest{
					Format: ociimage.FormatTart,
					Tart: ociimage.TartManifest{
						UncompressedDiskSize: 4096,
						DiskLayers:           []ociimage.TartDiskLayer{{}, {}, {}},
						NVRAMLayer:           ociimage.Descriptor{Size: 512},
						ConfigLayer:          ociimage.Descriptor{Size: 128},
						UploadTime:           "2026-05-09T00:00:00Z",
					},
				},
			},
			want: []string{"format: tart", "disk layers: 3", "upload time: 2026-05-09T00:00:00Z", "nvram:", "config:"},
		},
		{
			name: "cove empty manifest",
			plan: &pullPlan{
				Ref:      ref,
				VMName:   "dev",
				VMDir:    "/tmp/dev",
				Manifest: ociimage.ParsedManifest{Format: ociimage.FormatCove},
			},
			want: []string{"manifest: not provided"},
			deny: []string{"format: cove", "format: lume", "format: tart"},
		},
		{
			name: "cove with chunks",
			plan: &pullPlan{
				Ref:    ref,
				VMName: "dev",
				VMDir:  "/tmp/dev",
				Manifest: ociimage.ParsedManifest{
					Format:      ociimage.FormatCove,
					Annotations: ociimage.ManifestAnnotations{UncompressedDiskSize: 8192},
					Chunks:      []ociimage.Chunk{{}, {}},
					Blobs:       []ociimage.Descriptor{{}},
				},
			},
			want: []string{"format: cove", "chunks: 2", "metadata blobs: 1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			printPullDryRun(&buf, tt.plan)
			out := buf.String()
			for _, w := range tt.want {
				if !strings.Contains(out, w) {
					t.Errorf("missing %q\n--- got ---\n%s", w, out)
				}
			}
			for _, d := range tt.deny {
				if strings.Contains(out, d) {
					t.Errorf("unexpected %q\n--- got ---\n%s", d, out)
				}
			}
		})
	}
}
