package ociimage

import "testing"

func TestIsLumeManifest(t *testing.T) {
	tests := []struct {
		name string
		m    Manifest
		want bool
	}{
		{
			name: "lume tar layer with part.number",
			m: Manifest{
				SchemaVersion: 2,
				Annotations: map[string]string{
					"org.opencontainers.image.created": "2026-01-01T00:00:00Z",
				},
				Layers: []Descriptor{
					{
						MediaType: "application/vnd.oci.image.layer.v1.tar;part.number=1;part.total=2",
						Annotations: map[string]string{
							"org.opencontainers.image.title": "disk.img.part.aa",
						},
					},
				},
			},
			want: true,
		},
		{
			name: "cove manifest with chunk annotations",
			m: Manifest{
				SchemaVersion: 2,
				Annotations: map[string]string{
					CoveUncompressedDiskSize: "12345",
				},
				Layers: []Descriptor{
					{
						MediaType: MediaTypeLayer,
						Annotations: map[string]string{
							CoveChunkIndex: "0",
							CoveChunkTotal: "1",
						},
					},
				},
			},
			want: false,
		},
		{
			name: "cove via legacy lume disk-size annotation still rejected",
			m: Manifest{
				SchemaVersion: 2,
				Annotations: map[string]string{
					LumeUncompressedDiskSize: "12345",
				},
				Layers: []Descriptor{
					{MediaType: "application/vnd.oci.image.layer.v1.tar;part.number=1"},
				},
			},
			want: false,
		},
		{
			name: "no relevant layers",
			m: Manifest{
				SchemaVersion: 2,
				Layers: []Descriptor{
					{MediaType: MediaTypeImageConfig},
				},
			},
			want: false,
		},
		{
			name: "title-only disk part (no part.number param)",
			m: Manifest{
				SchemaVersion: 2,
				Layers: []Descriptor{
					{
						MediaType: "application/octet-stream",
						Annotations: map[string]string{
							"org.opencontainers.image.title": "disk.img.part.ab",
						},
					},
				},
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsLumeManifest(tt.m); got != tt.want {
				t.Errorf("IsLumeManifest = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseLumeManifestSortsAndExtractsSidecars(t *testing.T) {
	m := Manifest{
		SchemaVersion: 2,
		Annotations: map[string]string{
			"org.opencontainers.image.created": "2026-01-01T00:00:00Z",
		},
		Layers: []Descriptor{
			{
				MediaType: "application/vnd.oci.image.layer.v1.tar;part.number=2;part.total=3",
				Size:      100,
				Digest:    "sha256:bbb",
				Annotations: map[string]string{
					"org.opencontainers.image.title": "disk.img.part.ab",
				},
			},
			{
				MediaType: "application/octet-stream",
				Size:      11,
				Digest:    "sha256:nvr",
				Annotations: map[string]string{
					"org.opencontainers.image.title": "nvram.bin",
				},
			},
			{
				MediaType: "application/vnd.oci.image.layer.v1.tar;part.number=1;part.total=3",
				Size:      99,
				Digest:    "sha256:aaa",
				Annotations: map[string]string{
					"org.opencontainers.image.title": "disk.img.part.aa",
				},
			},
			{
				MediaType: "application/json",
				Size:      22,
				Digest:    "sha256:cfg",
				Annotations: map[string]string{
					"org.opencontainers.image.title": "config.json",
				},
			},
			{
				MediaType: "application/vnd.oci.image.layer.v1.tar;part.number=3;part.total=3",
				Size:      77,
				Digest:    "sha256:ccc",
				Annotations: map[string]string{
					"org.opencontainers.image.title": "disk.img.part.ac",
				},
			},
		},
	}
	if !IsLumeManifest(m) {
		t.Fatalf("IsLumeManifest = false, want true")
	}
	parsed, err := ParseManifest(m)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if parsed.Format != FormatLume {
		t.Fatalf("Format = %d, want FormatLume", parsed.Format)
	}
	if got := len(parsed.Lume.DiskParts); got != 3 {
		t.Fatalf("disk parts = %d, want 3", got)
	}
	wantOrder := []string{"sha256:aaa", "sha256:bbb", "sha256:ccc"}
	for i, p := range parsed.Lume.DiskParts {
		if p.Descriptor.Digest != wantOrder[i] {
			t.Errorf("part[%d] digest = %s, want %s", i, p.Descriptor.Digest, wantOrder[i])
		}
		if p.PartNumber != i+1 {
			t.Errorf("part[%d] PartNumber = %d, want %d", i, p.PartNumber, i+1)
		}
	}
	if parsed.Lume.NvramLayer == nil || parsed.Lume.NvramLayer.Digest != "sha256:nvr" {
		t.Errorf("NvramLayer = %v, want sha256:nvr", parsed.Lume.NvramLayer)
	}
	if parsed.Lume.ConfigLayer == nil || parsed.Lume.ConfigLayer.Digest != "sha256:cfg" {
		t.Errorf("ConfigLayer = %v, want sha256:cfg", parsed.Lume.ConfigLayer)
	}
}

func TestParseLumeManifestSortsByTitleWhenPartNumberMissing(t *testing.T) {
	m := Manifest{
		SchemaVersion: 2,
		Layers: []Descriptor{
			{
				MediaType: "application/octet-stream",
				Digest:    "sha256:bb",
				Annotations: map[string]string{
					"org.opencontainers.image.title": "disk.img.part.ab",
				},
			},
			{
				MediaType: "application/octet-stream",
				Digest:    "sha256:aa",
				Annotations: map[string]string{
					"org.opencontainers.image.title": "disk.img.part.aa",
				},
			},
		},
	}
	parsed, err := ParseManifest(m)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if parsed.Format != FormatLume {
		t.Fatalf("Format = %d, want FormatLume", parsed.Format)
	}
	got := []string{parsed.Lume.DiskParts[0].Descriptor.Digest, parsed.Lume.DiskParts[1].Descriptor.Digest}
	want := []string{"sha256:aa", "sha256:bb"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("part[%d] digest = %s, want %s", i, got[i], want[i])
		}
	}
}

func TestParseManifestStillRoutesCoveFormat(t *testing.T) {
	// Regression check: a cove manifest must route to FormatCove, not lume,
	// even though we added the dispatch.
	m := Manifest{
		SchemaVersion: 2,
		Annotations: map[string]string{
			CoveUncompressedDiskSize: "1024",
		},
		Layers: []Descriptor{
			{
				MediaType: MediaTypeLayer,
				Size:      1024,
				Digest:    "sha256:abc",
				Annotations: map[string]string{
					CoveChunkIndex:                "0",
					CoveChunkTotal:                "1",
					CoveUncompressedSize:          "1024",
					CoveUncompressedContentDigest: "sha256:def",
				},
			},
		},
	}
	parsed, err := ParseManifest(m)
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if parsed.Format != FormatCove {
		t.Fatalf("Format = %d, want FormatCove", parsed.Format)
	}
	if len(parsed.DiskLayers) != 1 {
		t.Fatalf("DiskLayers = %d, want 1", len(parsed.DiskLayers))
	}
}

func TestDecodeLumeConfig(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want LumeConfig
	}{
		{
			name: "camelCase",
			in: `{
				"os": "linux",
				"cpu": 4,
				"memory": "4G",
				"diskSize": "20G",
				"machineIdentifier": "abc123",
				"hardwareModel": "VMmodel",
				"macAddress": "aa:bb:cc:dd:ee:ff"
			}`,
			want: LumeConfig{
				OS:                "linux",
				CPU:               4,
				Memory:            "4G",
				DiskSize:          "20G",
				MachineIdentifier: "abc123",
				HardwareModel:     "VMmodel",
				MACAddress:        "aa:bb:cc:dd:ee:ff",
			},
		},
		{
			name: "PascalCase tolerated",
			in:   `{"OS":"macos","CPU":8,"Memory":"8G"}`,
			want: LumeConfig{OS: "macos", CPU: 8, Memory: "8G"},
		},
		{
			name: "extra fields ignored",
			in:   `{"os":"linux","display":{"width":1920}}`,
			want: LumeConfig{OS: "linux"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeLumeConfig([]byte(tt.in))
			if err != nil {
				t.Fatalf("DecodeLumeConfig: %v", err)
			}
			if got != tt.want {
				t.Errorf("DecodeLumeConfig = %+v, want %+v", got, tt.want)
			}
		})
	}
}
