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

// ManifestOptions controls manifest construction.
type ManifestOptions struct {
	UploadTime   string
	DiskSize     int64
	Chunks       []Chunk
	Blobs        []Blob
	BaseManifest string
	LumeCompat   bool
}

// BuildManifest builds a deterministic OCI manifest and its config JSON.
func BuildManifest(opts ManifestOptions) (Manifest, []byte, error) {
	var manifest Manifest
	if opts.DiskSize < 0 {
		return manifest, nil, fmt.Errorf("build manifest: negative disk size %d", opts.DiskSize)
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
	for _, c := range opts.Chunks {
		annotations := ChunkLayerAnnotations(c, len(opts.Chunks))
		if opts.LumeCompat {
			annotations = AddLumeCompatibility(annotations)
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
