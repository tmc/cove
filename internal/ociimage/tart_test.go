package ociimage

import (
	"strings"
	"testing"
)

// goldenTartManifest is a minimal but realistic tart manifest: stub OCI
// config blob, two 512 MiB disk-v2 chunks (annotations supply the
// uncompressed sizes), one nvram blob. Used by IsTartManifest, parse, and
// dispatch tests so they can't drift apart.
func goldenTartManifest() Manifest {
	return Manifest{
		SchemaVersion: 2,
		MediaType:     MediaTypeImageManifest,
		Config: Descriptor{
			MediaType: MediaTypeImageConfig,
			Size:      128,
			Digest:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		},
		Layers: []Descriptor{
			{
				MediaType: TartConfigMediaType,
				Size:      256,
				Digest:    "sha256:1111111111111111111111111111111111111111111111111111111111111111",
			},
			{
				MediaType: TartDiskV2MediaType,
				Size:      400_000_000,
				Digest:    "sha256:2222222222222222222222222222222222222222222222222222222222222222",
				Annotations: map[string]string{
					TartUncompressedSize:          "536870912", // 512 MiB
					TartUncompressedContentDigest: "sha256:aaaa",
				},
			},
			{
				MediaType: TartDiskV2MediaType,
				Size:      150_000_000,
				Digest:    "sha256:3333333333333333333333333333333333333333333333333333333333333333",
				Annotations: map[string]string{
					TartUncompressedSize:          "200000000",
					TartUncompressedContentDigest: "sha256:bbbb",
				},
			},
			{
				MediaType: TartNVRAMMediaType,
				Size:      262144,
				Digest:    "sha256:4444444444444444444444444444444444444444444444444444444444444444",
			},
		},
		Annotations: map[string]string{
			TartUncompressedDiskSize: "736870912", // 512 MiB + 200 MB
			TartUploadTime:           "2026-04-25T12:00:00Z",
		},
	}
}

func TestIsTartManifest(t *testing.T) {
	tests := []struct {
		name string
		m    Manifest
		want bool
	}{
		{
			name: "real tart manifest",
			m:    goldenTartManifest(),
			want: true,
		},
		{
			name: "no tart layers",
			m: Manifest{
				SchemaVersion: 2,
				Layers: []Descriptor{
					{MediaType: MediaTypeLayer, Digest: "sha256:abc"},
				},
			},
			want: false,
		},
		{
			name: "tart layers but cove annotation present",
			m: func() Manifest {
				m := goldenTartManifest()
				m.Annotations[CoveUncompressedDiskSize] = "12345"
				return m
			}(),
			want: false,
		},
		{
			name: "tart layers but lume tar layer mixed in",
			m: func() Manifest {
				m := goldenTartManifest()
				m.Layers = append(m.Layers, Descriptor{
					MediaType: LumeTarLayerMediaTypePrefix + ";part.number=1",
					Digest:    "sha256:dead",
				})
				return m
			}(),
			want: false,
		},
		{
			name: "tart layers but lume-aliased annotation present",
			m: func() Manifest {
				m := goldenTartManifest()
				m.Annotations[LumeUncompressedDiskSize] = "12345"
				return m
			}(),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsTartManifest(tt.m); got != tt.want {
				t.Errorf("IsTartManifest = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseTartManifestSuccess(t *testing.T) {
	m := goldenTartManifest()
	tart, err := ParseTartManifest(m)
	if err != nil {
		t.Fatalf("ParseTartManifest: %v", err)
	}
	if tart.ConfigLayer.Digest == "" {
		t.Error("ConfigLayer not populated")
	}
	if tart.NVRAMLayer.Digest == "" {
		t.Error("NVRAMLayer not populated")
	}
	if got, want := len(tart.DiskLayers), 2; got != want {
		t.Fatalf("DiskLayers count = %d, want %d", got, want)
	}
	if tart.DiskLayers[0].Offset != 0 {
		t.Errorf("first chunk offset = %d, want 0", tart.DiskLayers[0].Offset)
	}
	if tart.DiskLayers[1].Offset != 536870912 {
		t.Errorf("second chunk offset = %d, want 536870912", tart.DiskLayers[1].Offset)
	}
	if tart.UncompressedDiskSize != 736870912 {
		t.Errorf("UncompressedDiskSize = %d, want 736870912", tart.UncompressedDiskSize)
	}
	if tart.UploadTime != "2026-04-25T12:00:00Z" {
		t.Errorf("UploadTime = %q, want 2026-04-25T12:00:00Z", tart.UploadTime)
	}
}

func TestParseTartManifestErrors(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(m *Manifest)
		wantErr string
	}{
		{
			name: "missing config layer",
			mutate: func(m *Manifest) {
				m.Layers = m.Layers[1:] // drop config (index 0)
			},
			wantErr: "missing config layer",
		},
		{
			name: "missing nvram layer",
			mutate: func(m *Manifest) {
				m.Layers = m.Layers[:len(m.Layers)-1] // drop nvram (last)
			},
			wantErr: "missing nvram layer",
		},
		{
			name: "no disk layers",
			mutate: func(m *Manifest) {
				m.Layers = []Descriptor{m.Layers[0], m.Layers[len(m.Layers)-1]}
			},
			wantErr: "no disk.v2 layers",
		},
		{
			name: "duplicate config",
			mutate: func(m *Manifest) {
				m.Layers = append(m.Layers, Descriptor{MediaType: TartConfigMediaType, Digest: "sha256:dup"})
			},
			wantErr: "duplicate config layer",
		},
		{
			name: "duplicate nvram",
			mutate: func(m *Manifest) {
				m.Layers = append(m.Layers, Descriptor{MediaType: TartNVRAMMediaType, Digest: "sha256:dup"})
			},
			wantErr: "duplicate nvram layer",
		},
		{
			name: "disk-v1 rejected",
			mutate: func(m *Manifest) {
				m.Layers[1].MediaType = TartDiskV1MediaType
			},
			wantErr: "disk.v1 layer rejected",
		},
		{
			name: "disk layer missing uncompressed-size",
			mutate: func(m *Manifest) {
				delete(m.Layers[1].Annotations, TartUncompressedSize)
			},
			wantErr: "missing " + TartUncompressedSize,
		},
		{
			name: "disk layer missing content-digest",
			mutate: func(m *Manifest) {
				delete(m.Layers[1].Annotations, TartUncompressedContentDigest)
			},
			wantErr: "missing " + TartUncompressedContentDigest,
		},
		{
			name: "negative uncompressed-size",
			mutate: func(m *Manifest) {
				m.Layers[1].Annotations[TartUncompressedSize] = "-1"
			},
			wantErr: "negative uncompressed size",
		},
		{
			name: "manifest disk-size mismatch",
			mutate: func(m *Manifest) {
				m.Annotations[TartUncompressedDiskSize] = "999999999"
			},
			wantErr: "want disk size 999999999",
		},
		{
			name: "unknown tart mediaType",
			mutate: func(m *Manifest) {
				m.Layers = append(m.Layers, Descriptor{
					MediaType: TartMediaTypePrefix + "wat.v9",
					Digest:    "sha256:dead",
				})
			},
			wantErr: "unknown tart mediaType",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := goldenTartManifest()
			tt.mutate(&m)
			_, err := ParseTartManifest(m)
			if err == nil {
				t.Fatalf("ParseTartManifest: expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("ParseTartManifest error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestParseTartManifestInfersDiskSize(t *testing.T) {
	// Older fixtures may not carry the manifest-level uncompressed-disk-size
	// annotation. The parser should sum the per-layer sizes instead of
	// rejecting.
	m := goldenTartManifest()
	delete(m.Annotations, TartUncompressedDiskSize)
	tart, err := ParseTartManifest(m)
	if err != nil {
		t.Fatalf("ParseTartManifest: %v", err)
	}
	if tart.UncompressedDiskSize != 736870912 {
		t.Errorf("UncompressedDiskSize = %d, want 736870912", tart.UncompressedDiskSize)
	}
}

func TestParseTartManifestTolerantToExtraLayers(t *testing.T) {
	// A non-tart mediaType inside a tart manifest is pass-through (cove
	// ignores it). The parser should not error.
	m := goldenTartManifest()
	m.Layers = append(m.Layers, Descriptor{
		MediaType: MediaTypeLayer,
		Digest:    "sha256:extra",
	})
	tart, err := ParseTartManifest(m)
	if err != nil {
		t.Fatalf("ParseTartManifest: %v", err)
	}
	if len(tart.DiskLayers) != 2 {
		t.Errorf("DiskLayers = %d, want 2 (extra non-tart layer must not register)", len(tart.DiskLayers))
	}
}

func TestParseManifestDispatchesTart(t *testing.T) {
	m := goldenTartManifest()
	parsed, err := ParseManifest(m)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if parsed.Format != FormatTart {
		t.Errorf("Format = %v, want FormatTart", parsed.Format)
	}
	if len(parsed.Tart.DiskLayers) != 2 {
		t.Errorf("Tart.DiskLayers = %d, want 2", len(parsed.Tart.DiskLayers))
	}
	if len(parsed.Chunks) != 0 {
		t.Errorf("cove Chunks unexpectedly populated: %d", len(parsed.Chunks))
	}
	if len(parsed.Lume.DiskParts) != 0 {
		t.Errorf("Lume DiskParts unexpectedly populated: %d", len(parsed.Lume.DiskParts))
	}
}
