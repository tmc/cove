package ociimage

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeManifestAnnotations(t *testing.T) {
	tests := []struct {
		name    string
		in      map[string]string
		want    ManifestAnnotations
		wantErr string
	}{
		{
			name: "cove native",
			in: map[string]string{
				CoveUploadTime:           "2026-04-16T12:00:00Z",
				CoveUncompressedDiskSize: "96636764160",
				CoveHWModelDigest:        "sha256:hw",
				CoveAuxDigest:            "sha256:aux",
				CoveBaseManifest:         "sha256:base",
			},
			want: ManifestAnnotations{
				UploadTime:           "2026-04-16T12:00:00Z",
				UncompressedDiskSize: 96636764160,
				HWModelDigest:        "sha256:hw",
				AuxDigest:            "sha256:aux",
				BaseManifest:         "sha256:base",
			},
		},
		{
			name: "lume only",
			in: map[string]string{
				LumeUploadTime:           "2026-04-16T12:00:00Z",
				LumeUncompressedDiskSize: "42",
			},
			want: ManifestAnnotations{
				UploadTime:           "2026-04-16T12:00:00Z",
				UncompressedDiskSize: 42,
			},
		},
		{
			name: "cove wins",
			in: map[string]string{
				CoveUploadTime:           "cove-time",
				LumeUploadTime:           "lume-time",
				CoveUncompressedDiskSize: "10",
				LumeUncompressedDiskSize: "20",
			},
			want: ManifestAnnotations{
				UploadTime:           "cove-time",
				UncompressedDiskSize: 10,
			},
		},
		{
			name:    "missing disk size",
			in:      map[string]string{CoveUploadTime: "2026-04-16T12:00:00Z"},
			wantErr: CoveUncompressedDiskSize,
		},
		{
			name: "bad disk size",
			in: map[string]string{
				CoveUncompressedDiskSize: "not-an-int",
			},
			wantErr: "parse annotation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeManifestAnnotations(tt.in)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("NormalizeManifestAnnotations() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeManifestAnnotations(): %v", err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeManifestAnnotations() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestNormalizeLayerAnnotations(t *testing.T) {
	tests := []struct {
		name    string
		in      map[string]string
		want    LayerAnnotations
		wantErr string
	}{
		{
			name: "cove chunk",
			in: map[string]string{
				CoveRole:                      "disk",
				CoveUncompressedSize:          "536870912",
				CoveUncompressedContentDigest: "sha256:chunk",
				CoveChunkIndex:                "2",
				CoveChunkTotal:                "10",
				CoveZeroChunk:                 "true",
			},
			want: LayerAnnotations{
				Role:                      "disk",
				UncompressedSize:          536870912,
				UncompressedContentDigest: "sha256:chunk",
				ChunkIndex:                2,
				ChunkTotal:                10,
				ZeroChunk:                 true,
			},
		},
		{
			name: "lume chunk",
			in: map[string]string{
				LumeRole:                      "disk",
				LumeUncompressedSize:          "1024",
				LumeUncompressedContentDigest: "sha256:lume",
				LumeChunkIndex:                "0",
				LumeChunkTotal:                "1",
			},
			want: LayerAnnotations{
				Role:                      "disk",
				UncompressedSize:          1024,
				UncompressedContentDigest: "sha256:lume",
				ChunkIndex:                0,
				ChunkTotal:                1,
			},
		},
		{
			name: "cove wins",
			in: map[string]string{
				CoveUncompressedSize:          "10",
				LumeUncompressedSize:          "20",
				CoveUncompressedContentDigest: "sha256:cove",
				LumeUncompressedContentDigest: "sha256:lume",
				CoveChunkIndex:                "3",
				LumeChunkIndex:                "4",
				CoveChunkTotal:                "5",
				LumeChunkTotal:                "6",
			},
			want: LayerAnnotations{
				UncompressedSize:          10,
				UncompressedContentDigest: "sha256:cove",
				ChunkIndex:                3,
				ChunkTotal:                5,
			},
		},
		{
			name: "role only",
			in:   map[string]string{CoveRole: "nvram"},
			want: LayerAnnotations{Role: "nvram"},
		},
		{
			name: "partial chunk",
			in: map[string]string{
				CoveUncompressedSize: "10",
				CoveChunkIndex:       "0",
				CoveChunkTotal:       "1",
			},
			wantErr: CoveUncompressedContentDigest,
		},
		{
			name: "negative chunk index",
			in: map[string]string{
				CoveUncompressedSize:          "10",
				CoveUncompressedContentDigest: "sha256:chunk",
				CoveChunkIndex:                "-1",
				CoveChunkTotal:                "1",
			},
			wantErr: "negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeLayerAnnotations(tt.in)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("NormalizeLayerAnnotations() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeLayerAnnotations(): %v", err)
			}
			if got != tt.want {
				t.Fatalf("NormalizeLayerAnnotations() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestNormalizeAnnotationKeys(t *testing.T) {
	got := NormalizeAnnotationKeys(map[string]string{
		LumeChunkIndex:                "9",
		CoveChunkIndex:                "1",
		LumeUncompressedContentDigest: "sha256:lume",
		"example":                     "keep",
	})
	want := map[string]string{
		CoveChunkIndex:                "1",
		CoveUncompressedContentDigest: "sha256:lume",
		"example":                     "keep",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeAnnotationKeys() = %#v, want %#v", got, want)
	}
}

func TestAddLumeCompatibility(t *testing.T) {
	in := map[string]string{
		CoveChunkIndex:                "1",
		CoveUncompressedContentDigest: "sha256:cove",
		LumeUncompressedContentDigest: "sha256:existing",
		"example":                     "keep",
	}
	got := AddLumeCompatibility(in)

	want := map[string]string{
		CoveChunkIndex:                "1",
		LumeChunkIndex:                "1",
		CoveUncompressedContentDigest: "sha256:cove",
		LumeUncompressedContentDigest: "sha256:existing",
		"example":                     "keep",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AddLumeCompatibility() = %#v, want %#v", got, want)
	}
	if _, ok := in[LumeChunkIndex]; ok {
		t.Fatal("AddLumeCompatibility mutated input")
	}
}
