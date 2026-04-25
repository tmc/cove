package ociimage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
)

const (
	MediaTypeImageManifest = "application/vnd.oci.image.manifest.v1+json"
	MediaTypeImageConfig   = "application/vnd.oci.image.config.v1+json"
	MediaTypeLayer         = "application/octet-stream"
)

// Descriptor is an OCI content descriptor.
type Descriptor struct {
	MediaType   string            `json:"mediaType"`
	Size        int64             `json:"size"`
	Digest      string            `json:"digest"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// Manifest is the OCI image manifest shape cove writes.
type Manifest struct {
	SchemaVersion int               `json:"schemaVersion"`
	MediaType     string            `json:"mediaType"`
	Config        Descriptor        `json:"config"`
	Layers        []Descriptor      `json:"layers"`
	Annotations   map[string]string `json:"annotations,omitempty"`
}

// Blob describes one non-disk file included in the image.
type Blob struct {
	Role   string
	Size   int64
	Digest string
}

// DiskLayer pairs a disk chunk with its OCI layer descriptor.
type DiskLayer struct {
	Chunk      Chunk
	Descriptor Descriptor
}

// ManifestOptions controls manifest construction.
type ManifestOptions struct {
	UploadTime       string
	DiskSize         int64
	Chunks           []Chunk
	ChunkDescriptors []Descriptor
	Blobs            []Blob
	BaseManifest     string
	LumeCompat       bool
}

// ManifestFormat identifies how the disk image is laid out across layers.
type ManifestFormat int

const (
	// FormatCove is the LZ4-chunk-with-offset-annotation layout cove writes.
	FormatCove ManifestFormat = iota
	// FormatLume is the tar-split layout used by lume's ghcr.io images.
	FormatLume
)

// ParsedManifest is the normalized disk and sidecar metadata from a manifest.
type ParsedManifest struct {
	Format      ManifestFormat
	Annotations ManifestAnnotations
	Chunks      []Chunk
	DiskLayers  []DiskLayer
	Blobs       []Descriptor
	// Lume is populated when Format == FormatLume.
	Lume LumeManifest
}

// BuildManifest builds a deterministic OCI manifest and its config JSON.
func BuildManifest(opts ManifestOptions) (Manifest, []byte, error) {
	var manifest Manifest
	if opts.DiskSize < 0 {
		return manifest, nil, fmt.Errorf("build manifest: negative disk size %d", opts.DiskSize)
	}
	if len(opts.ChunkDescriptors) != 0 && len(opts.ChunkDescriptors) != len(opts.Chunks) {
		return manifest, nil, fmt.Errorf("build manifest: chunk descriptors %d, want %d", len(opts.ChunkDescriptors), len(opts.Chunks))
	}
	configJSON, err := json.Marshal(imageConfig{
		Created: opts.UploadTime,
		RootFS:  imageRootFS{Type: "layers", DiffIDs: diffIDs(opts.Chunks)},
	})
	if err != nil {
		return manifest, nil, fmt.Errorf("build manifest config: %w", err)
	}

	annotations := map[string]string{
		CoveUncompressedDiskSize: strconv.FormatInt(opts.DiskSize, 10),
	}
	if opts.UploadTime != "" {
		annotations[CoveUploadTime] = opts.UploadTime
	}
	if opts.BaseManifest != "" {
		annotations[CoveBaseManifest] = opts.BaseManifest
	}
	for _, b := range opts.Blobs {
		switch b.Role {
		case "hw-model":
			annotations[CoveHWModelDigest] = b.Digest
		case "nvram":
			annotations[CoveAuxDigest] = b.Digest
		}
	}
	if opts.LumeCompat {
		annotations = AddLumeCompatibility(annotations)
	}

	layers := make([]Descriptor, 0, len(opts.Blobs)+len(opts.Chunks))
	for _, b := range opts.Blobs {
		if err := validateBlob(b); err != nil {
			return manifest, nil, err
		}
		annotations := map[string]string{
			CoveRole: b.Role,
		}
		if opts.LumeCompat {
			annotations = AddLumeCompatibility(annotations)
		}
		layers = append(layers, Descriptor{
			MediaType:   MediaTypeLayer,
			Size:        b.Size,
			Digest:      b.Digest,
			Annotations: annotations,
		})
	}
	for i, c := range opts.Chunks {
		annotations := ChunkLayerAnnotations(c, len(opts.Chunks))
		if opts.LumeCompat {
			annotations = AddLumeCompatibility(annotations)
		}
		if len(opts.ChunkDescriptors) != 0 {
			desc := opts.ChunkDescriptors[i]
			if desc.MediaType == "" {
				desc.MediaType = MediaTypeLayer
			}
			if desc.Digest == "" {
				desc.Digest = c.Digest
			}
			desc.Annotations = annotations
			layers = append(layers, desc)
			continue
		}
		layers = append(layers, Descriptor{
			MediaType:   MediaTypeLayer,
			Size:        c.Size,
			Digest:      c.Digest,
			Annotations: annotations,
		})
	}

	return Manifest{
		SchemaVersion: 2,
		MediaType:     MediaTypeImageManifest,
		Config: Descriptor{
			MediaType: MediaTypeImageConfig,
			Size:      int64(len(configJSON)),
			Digest:    digestBytes(configJSON),
		},
		Layers:      layers,
		Annotations: annotations,
	}, configJSON, nil
}

// ParseManifest validates an OCI manifest and returns cove-normalized metadata.
// If the manifest is in lume's tar-split format (no cove annotations + tar
// part layers), Format is set to FormatLume and Lume is populated; the cove
// chunk fields stay empty.
func ParseManifest(m Manifest) (ParsedManifest, error) {
	var out ParsedManifest
	if m.SchemaVersion != 2 {
		return out, fmt.Errorf("parse manifest: schema version %d, want 2", m.SchemaVersion)
	}
	if IsLumeManifest(m) {
		lume, err := ParseLumeManifest(m)
		if err != nil {
			return out, err
		}
		out.Format = FormatLume
		out.Lume = lume
		return out, nil
	}
	annotations, err := NormalizeManifestAnnotations(m.Annotations)
	if err != nil {
		return out, fmt.Errorf("parse manifest: %w", err)
	}
	out.Annotations = annotations

	diskLayersByIndex := make(map[int]DiskLayer)
	chunkTotal := -1
	for _, layer := range m.Layers {
		ann, err := NormalizeLayerAnnotations(layer.Annotations)
		if err != nil {
			return out, fmt.Errorf("parse manifest layer: %w", err)
		}
		if ann.UncompressedContentDigest == "" {
			if ann.Role != "" {
				out.Blobs = append(out.Blobs, layer)
			}
			continue
		}
		if ann.Role != "" && ann.Role != "disk" {
			return out, fmt.Errorf("parse manifest layer: chunk role %q, want disk", ann.Role)
		}
		if ann.ChunkTotal <= 0 {
			return out, fmt.Errorf("parse manifest layer: invalid chunk total %d", ann.ChunkTotal)
		}
		if ann.ChunkIndex < 0 || ann.ChunkIndex >= ann.ChunkTotal {
			return out, fmt.Errorf("parse manifest layer: chunk index %d out of range %d", ann.ChunkIndex, ann.ChunkTotal)
		}
		if chunkTotal == -1 {
			chunkTotal = ann.ChunkTotal
		} else if chunkTotal != ann.ChunkTotal {
			return out, fmt.Errorf("parse manifest layer: chunk total %d, want %d", ann.ChunkTotal, chunkTotal)
		}
		if _, ok := diskLayersByIndex[ann.ChunkIndex]; ok {
			return out, fmt.Errorf("parse manifest layer: duplicate chunk index %d", ann.ChunkIndex)
		}
		diskLayersByIndex[ann.ChunkIndex] = DiskLayer{
			Chunk: Chunk{
				Index:  ann.ChunkIndex,
				Size:   ann.UncompressedSize,
				Digest: ann.UncompressedContentDigest,
				Zero:   ann.ZeroChunk,
			},
			Descriptor: layer,
		}
	}
	if chunkTotal == -1 {
		if annotations.UncompressedDiskSize != 0 {
			return out, fmt.Errorf("parse manifest: no disk chunks")
		}
		return out, nil
	}
	if len(diskLayersByIndex) != chunkTotal {
		return out, fmt.Errorf("parse manifest: chunks %d, want %d", len(diskLayersByIndex), chunkTotal)
	}
	out.Chunks = make([]Chunk, chunkTotal)
	out.DiskLayers = make([]DiskLayer, chunkTotal)
	var offset int64
	for i := 0; i < chunkTotal; i++ {
		layer, ok := diskLayersByIndex[i]
		if !ok {
			return out, fmt.Errorf("parse manifest: missing chunk %d", i)
		}
		chunk := layer.Chunk
		chunk.Offset = offset
		out.Chunks[i] = chunk
		layer.Chunk = chunk
		out.DiskLayers[i] = layer
		offset += chunk.Size
	}
	if offset != annotations.UncompressedDiskSize {
		return out, fmt.Errorf("parse manifest: chunk bytes %d, want disk size %d", offset, annotations.UncompressedDiskSize)
	}
	return out, nil
}

// DigestFile returns the sha256 digest and size of path.
func DigestFile(path string) (Blob, error) {
	var out Blob
	f, err := os.Open(path)
	if err != nil {
		return out, fmt.Errorf("digest file: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return out, fmt.Errorf("digest file: %w", err)
	}
	out.Size = n
	out.Digest = "sha256:" + hex.EncodeToString(h.Sum(nil))
	return out, nil
}

type imageConfig struct {
	Created string      `json:"created,omitempty"`
	RootFS  imageRootFS `json:"rootfs"`
}

type imageRootFS struct {
	Type    string   `json:"type"`
	DiffIDs []string `json:"diff_ids,omitempty"`
}

func diffIDs(chunks []Chunk) []string {
	out := make([]string, len(chunks))
	for i, c := range chunks {
		out[i] = c.Digest
	}
	return out
}

func validateBlob(b Blob) error {
	if b.Role == "" {
		return fmt.Errorf("build manifest: missing blob role")
	}
	if b.Size < 0 {
		return fmt.Errorf("build manifest: negative blob size %d", b.Size)
	}
	if b.Digest == "" {
		return fmt.Errorf("build manifest: missing blob digest")
	}
	return nil
}
