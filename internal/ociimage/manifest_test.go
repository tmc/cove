package ociimage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestBuildManifest(t *testing.T) {
	chunks := []Chunk{
		{Index: 0, Offset: 0, Size: 4, Digest: testDigest([]byte{1, 2, 3, 4})},
		{Index: 1, Offset: 4, Size: 2, Digest: testDigest([]byte{5, 6})},
	}
	aux := Blob{Role: "nvram", Size: 3, Digest: testDigest([]byte("aux"))}
	hw := Blob{Role: "hw-model", Size: 2, Digest: testDigest([]byte("hw"))}

	got, configJSON, err := BuildManifest(ManifestOptions{
		UploadTime: "2026-04-23T00:00:00Z",
		DiskSize:   6,
		Chunks:     chunks,
		Blobs:      []Blob{aux, hw},
		LumeCompat: true,
	})
	if err != nil {
		t.Fatalf("BuildManifest(): %v", err)
	}
	if got.SchemaVersion != 2 || got.MediaType != MediaTypeImageManifest {
		t.Fatalf("manifest header = (%d, %q)", got.SchemaVersion, got.MediaType)
	}
	if got.Config.MediaType != MediaTypeImageConfig || got.Config.Size != int64(len(configJSON)) || got.Config.Digest != testDigest(configJSON) {
		t.Fatalf("config descriptor = %#v, config len %d", got.Config, len(configJSON))
	}
	if len(got.Layers) != 4 {
		t.Fatalf("layers = %d, want 4", len(got.Layers))
	}
	if got.Layers[0].Annotations[CoveRole] != "nvram" || got.Layers[0].Digest != aux.Digest {
		t.Fatalf("aux layer = %#v", got.Layers[0])
	}
	if got.Layers[0].Annotations[LumeRole] != "nvram" {
		t.Fatalf("aux layer missing lume role: %#v", got.Layers[0].Annotations)
	}
	if got.Layers[1].Annotations[CoveRole] != "hw-model" || got.Layers[1].Digest != hw.Digest {
		t.Fatalf("hw layer = %#v", got.Layers[1])
	}
	if got.Layers[2].Annotations[CoveChunkIndex] != "0" || got.Layers[2].Annotations[LumeChunkIndex] != "0" {
		t.Fatalf("chunk layer annotations = %#v", got.Layers[2].Annotations)
	}

	wantAnnotations := map[string]string{
		CoveUploadTime:           "2026-04-23T00:00:00Z",
		LumeUploadTime:           "2026-04-23T00:00:00Z",
		CoveUncompressedDiskSize: "6",
		LumeUncompressedDiskSize: "6",
		CoveAuxDigest:            aux.Digest,
		CoveHWModelDigest:        hw.Digest,
	}
	if !reflect.DeepEqual(got.Annotations, wantAnnotations) {
		t.Fatalf("annotations = %#v, want %#v", got.Annotations, wantAnnotations)
	}

	var cfg imageConfig
	if err := json.Unmarshal(configJSON, &cfg); err != nil {
		t.Fatalf("Unmarshal(config): %v", err)
	}
	if !reflect.DeepEqual(cfg.RootFS.DiffIDs, []string{chunks[0].Digest, chunks[1].Digest}) {
		t.Fatalf("diff ids = %#v", cfg.RootFS.DiffIDs)
	}
}

func TestBuildManifestRejectsInvalid(t *testing.T) {
	tests := []struct {
		name string
		opts ManifestOptions
	}{
		{name: "negative disk", opts: ManifestOptions{DiskSize: -1}},
		{name: "missing role", opts: ManifestOptions{Blobs: []Blob{{Digest: testDigest(nil)}}}},
		{name: "missing digest", opts: ManifestOptions{Blobs: []Blob{{Role: "nvram"}}}},
		{name: "negative blob", opts: ManifestOptions{Blobs: []Blob{{Role: "nvram", Size: -1, Digest: testDigest(nil)}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := BuildManifest(tt.opts)
			if err == nil {
				t.Fatal("BuildManifest() error = nil, want error")
			}
		})
	}
}

func TestParseManifest(t *testing.T) {
	chunks := []Chunk{
		{Index: 0, Offset: 0, Size: 4, Digest: testDigest([]byte{1, 2, 3, 4})},
		{Index: 1, Offset: 4, Size: 2, Digest: testDigest([]byte{5, 6})},
	}
	manifest, _, err := BuildManifest(ManifestOptions{
		UploadTime: "2026-04-23T00:00:00Z",
		DiskSize:   6,
		Chunks:     chunks,
		Blobs: []Blob{
			{Role: "nvram", Size: 3, Digest: testDigest([]byte("aux"))},
		},
		LumeCompat: true,
	})
	if err != nil {
		t.Fatalf("BuildManifest(): %v", err)
	}

	got, err := ParseManifest(manifest)
	if err != nil {
		t.Fatalf("ParseManifest(): %v", err)
	}
	if got.Annotations.UncompressedDiskSize != 6 {
		t.Fatalf("disk size = %d, want 6", got.Annotations.UncompressedDiskSize)
	}
	if !reflect.DeepEqual(got.Chunks, chunks) {
		t.Fatalf("chunks = %#v, want %#v", got.Chunks, chunks)
	}
	if len(got.DiskLayers) != len(chunks) {
		t.Fatalf("disk layers = %d, want %d", len(got.DiskLayers), len(chunks))
	}
	if !reflect.DeepEqual(got.DiskLayers[0].Chunk, chunks[0]) || got.DiskLayers[0].Descriptor.Digest != manifest.Layers[1].Digest {
		t.Fatalf("disk layer 0 = %#v, want chunk %#v descriptor %#v", got.DiskLayers[0], chunks[0], manifest.Layers[1])
	}
	if len(got.Blobs) != 1 || got.Blobs[0].Annotations[CoveRole] != "nvram" {
		t.Fatalf("blobs = %#v", got.Blobs)
	}
}

func TestParseManifestAcceptsLumeAnnotations(t *testing.T) {
	digest := testDigest([]byte{1, 2, 3})
	manifest := Manifest{
		SchemaVersion: 2,
		Annotations: map[string]string{
			LumeUncompressedDiskSize: "3",
		},
		Layers: []Descriptor{
			{
				Annotations: map[string]string{
					LumeRole:                      "disk",
					LumeUncompressedSize:          "3",
					LumeUncompressedContentDigest: digest,
					LumeChunkIndex:                "0",
					LumeChunkTotal:                "1",
				},
			},
		},
	}

	got, err := ParseManifest(manifest)
	if err != nil {
		t.Fatalf("ParseManifest(): %v", err)
	}
	want := []Chunk{{Index: 0, Offset: 0, Size: 3, Digest: digest}}
	if !reflect.DeepEqual(got.Chunks, want) {
		t.Fatalf("chunks = %#v, want %#v", got.Chunks, want)
	}
	if len(got.DiskLayers) != 1 || !reflect.DeepEqual(got.DiskLayers[0].Chunk, want[0]) {
		t.Fatalf("disk layers = %#v, want chunk %#v", got.DiskLayers, want[0])
	}
}

func TestParseManifestPreservesZeroChunks(t *testing.T) {
	digest := testDigest([]byte{0, 0, 0})
	manifest := Manifest{
		SchemaVersion: 2,
		Annotations: map[string]string{
			CoveUncompressedDiskSize: "3",
		},
		Layers: []Descriptor{
			{
				Annotations: map[string]string{
					CoveRole:                      "disk",
					CoveUncompressedSize:          "3",
					CoveUncompressedContentDigest: digest,
					CoveChunkIndex:                "0",
					CoveChunkTotal:                "1",
					CoveZeroChunk:                 "true",
				},
			},
		},
	}

	got, err := ParseManifest(manifest)
	if err != nil {
		t.Fatalf("ParseManifest(): %v", err)
	}
	if len(got.Chunks) != 1 || !got.Chunks[0].Zero {
		t.Fatalf("chunks = %#v, want zero chunk", got.Chunks)
	}
	if len(got.DiskLayers) != 1 || !got.DiskLayers[0].Chunk.Zero {
		t.Fatalf("disk layers = %#v, want zero chunk", got.DiskLayers)
	}
}

func TestParseManifestRejectsInvalid(t *testing.T) {
	digest := testDigest([]byte{1, 2, 3})
	base := Manifest{
		SchemaVersion: 2,
		Annotations: map[string]string{
			CoveUncompressedDiskSize: "3",
		},
		Layers: []Descriptor{
			{
				Annotations: map[string]string{
					CoveRole:                      "disk",
					CoveUncompressedSize:          "3",
					CoveUncompressedContentDigest: digest,
					CoveChunkIndex:                "0",
					CoveChunkTotal:                "1",
				},
			},
		},
	}
	tests := []struct {
		name   string
		mutate func(Manifest) Manifest
	}{
		{name: "schema", mutate: func(m Manifest) Manifest {
			m.SchemaVersion = 1
			return m
		}},
		{name: "missing disk size", mutate: func(m Manifest) Manifest {
			m.Annotations = nil
			return m
		}},
		{name: "duplicate chunk", mutate: func(m Manifest) Manifest {
			m.Layers = append(m.Layers, m.Layers[0])
			m.Layers[1].Annotations = cloneStringMap(m.Layers[0].Annotations)
			m.Layers[1].Annotations[CoveChunkTotal] = "2"
			m.Layers[0].Annotations = cloneStringMap(m.Layers[0].Annotations)
			m.Layers[0].Annotations[CoveChunkTotal] = "2"
			return m
		}},
		{name: "missing chunk", mutate: func(m Manifest) Manifest {
			m.Layers[0].Annotations = cloneStringMap(m.Layers[0].Annotations)
			m.Layers[0].Annotations[CoveChunkTotal] = "2"
			return m
		}},
		{name: "size mismatch", mutate: func(m Manifest) Manifest {
			m.Annotations = cloneStringMap(m.Annotations)
			m.Annotations[CoveUncompressedDiskSize] = "4"
			return m
		}},
		{name: "wrong chunk role", mutate: func(m Manifest) Manifest {
			m.Layers[0].Annotations = cloneStringMap(m.Layers[0].Annotations)
			m.Layers[0].Annotations[CoveRole] = "nvram"
			return m
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseManifest(tt.mutate(base))
			if err == nil {
				t.Fatal("ParseManifest() error = nil, want error")
			}
		})
	}
}

func TestDigestFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aux.img")
	data := []byte("aux-data")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := DigestFile(path)
	if err != nil {
		t.Fatalf("DigestFile(): %v", err)
	}
	if got.Size != int64(len(data)) || got.Digest != testDigest(data) {
		t.Fatalf("DigestFile() = %#v, want size %d digest %s", got, len(data), testDigest(data))
	}
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
