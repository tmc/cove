// tart.go — detection and parsing for cirruslabs/tart OCI manifests.
//
// Tart packages a macOS VM as one config layer
// (application/vnd.cirruslabs.tart.config.v1), N disk chunks at 512 MiB
// each (application/vnd.cirruslabs.tart.disk.v2), and one nvram blob
// (application/vnd.cirruslabs.tart.nvram.v1). Each disk chunk carries
// the uncompressed-size and uncompressed-content-digest annotations
// under the org.cirruslabs.tart prefix.
//
// See docs/research/cove-tart-compat.md for the full format reference.

package ociimage

import (
	"fmt"
	"strconv"
	"strings"
)

// Tart manifest media types.
const (
	TartConfigMediaType   = "application/vnd.cirruslabs.tart.config.v1"
	TartDiskV2MediaType   = "application/vnd.cirruslabs.tart.disk.v2"
	TartDiskV1MediaType   = "application/vnd.cirruslabs.tart.disk.v1"
	TartNVRAMMediaType    = "application/vnd.cirruslabs.tart.nvram.v1"
	TartMediaTypePrefix   = "application/vnd.cirruslabs.tart."
)

// Tart annotation keys (manifest-level and per-layer).
const (
	TartUncompressedDiskSize      = "org.cirruslabs.tart.uncompressed-disk-size"
	TartUncompressedSize          = "org.cirruslabs.tart.uncompressed-size"
	TartUncompressedContentDigest = "org.cirruslabs.tart.uncompressed-content-digest"
	TartUploadTime                = "org.cirruslabs.tart.upload-time"
)

// TartDiskLayer pairs a tart disk descriptor with its decoded annotations.
type TartDiskLayer struct {
	Descriptor       Descriptor
	UncompressedSize int64
	// UncompressedContentDigest is sha256 of the *uncompressed* chunk bytes.
	// Tart uses this for de-duplication against the destination disk during
	// pull; cove preserves it so reverse-trip pushes match.
	UncompressedContentDigest string
	// Offset is the cumulative uncompressed offset of this chunk in the
	// reconstructed disk image, filled by ParseTartManifest.
	Offset int64
}

// TartManifest is the normalized view of a tart manifest.
type TartManifest struct {
	// ConfigLayer is the VMConfig JSON layer
	// (application/vnd.cirruslabs.tart.config.v1). Required.
	ConfigLayer Descriptor
	// NVRAMLayer is the nvram blob layer. Required.
	NVRAMLayer Descriptor
	// DiskLayers are tart disk-v2 chunks in manifest order.
	DiskLayers []TartDiskLayer
	// UncompressedDiskSize is the total uncompressed disk size in bytes,
	// taken from the manifest's org.cirruslabs.tart.uncompressed-disk-size
	// annotation. Cove uses this to pre-truncate the destination file so
	// unwritten regions stay sparse.
	UncompressedDiskSize int64
	// UploadTime is the manifest's upload-time annotation, ISO8601. Empty
	// when missing — tart populates it but cove tolerates older fixtures.
	UploadTime string
}

// IsTartManifest reports whether m is a cirruslabs/tart image. Returns true
// iff at least one layer mediaType starts with the tart prefix and the
// manifest carries no cove or lume markers — see the dispatch rule in
// docs/research/cove-tart-compat.md.
func IsTartManifest(m Manifest) bool {
	if hasCoveOrLumeMarkers(m) {
		return false
	}
	for _, layer := range m.Layers {
		if strings.HasPrefix(layer.MediaType, TartMediaTypePrefix) {
			return true
		}
	}
	return false
}

// hasCoveOrLumeMarkers reports whether m carries any cove-style annotation
// or any lume tar-split layer. ParseManifest dispatches Lume → Tart → Cove,
// but tart manifests must lack both cove markers and lume layers; this
// helper centralises the rejection.
func hasCoveOrLumeMarkers(m Manifest) bool {
	for _, key := range []string{
		CoveUncompressedDiskSize,
		CoveHWModelDigest,
		CoveAuxDigest,
		CoveUploadTime,
	} {
		if _, ok := m.Annotations[key]; ok {
			return true
		}
		if lumeKey, ok := coveToLume[key]; ok {
			if _, ok := m.Annotations[lumeKey]; ok {
				return true
			}
		}
	}
	for _, layer := range m.Layers {
		if isLumeTarLayer(layer) {
			return true
		}
	}
	return false
}

// ParseTartManifest extracts cirruslabs/tart metadata. Returns an error if
// any required layer (config, nvram, ≥1 disk-v2) is missing or carries
// invalid annotations.
//
// Layer dispatch is by mediaType: tart pins exact strings rather than
// using prefix-with-parameters like lume, so a strict match is correct.
//
// disk-v1 layers are explicitly rejected — tart itself stopped writing
// them; supporting them would require extra round-tripping of the legacy
// per-chunk format that cove gains nothing from.
func ParseTartManifest(m Manifest) (TartManifest, error) {
	var out TartManifest
	if v, ok := m.Annotations[TartUncompressedDiskSize]; ok {
		size, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return out, fmt.Errorf("parse tart manifest: invalid %s %q: %w", TartUncompressedDiskSize, v, err)
		}
		if size < 0 {
			return out, fmt.Errorf("parse tart manifest: negative %s %d", TartUncompressedDiskSize, size)
		}
		out.UncompressedDiskSize = size
	}
	out.UploadTime = m.Annotations[TartUploadTime]

	var configFound, nvramFound bool
	for _, layer := range m.Layers {
		switch layer.MediaType {
		case TartConfigMediaType:
			if configFound {
				return out, fmt.Errorf("parse tart manifest: duplicate config layer")
			}
			configFound = true
			out.ConfigLayer = layer
		case TartNVRAMMediaType:
			if nvramFound {
				return out, fmt.Errorf("parse tart manifest: duplicate nvram layer")
			}
			nvramFound = true
			out.NVRAMLayer = layer
		case TartDiskV2MediaType:
			disk, err := parseTartDiskLayer(layer)
			if err != nil {
				return out, err
			}
			out.DiskLayers = append(out.DiskLayers, disk)
		case TartDiskV1MediaType:
			return out, fmt.Errorf("parse tart manifest: disk.v1 layer rejected (legacy format)")
		default:
			if strings.HasPrefix(layer.MediaType, TartMediaTypePrefix) {
				return out, fmt.Errorf("parse tart manifest: unknown tart mediaType %q", layer.MediaType)
			}
			// Non-tart mediaTypes inside a tart manifest are tolerated as
			// pass-through blobs — tart's own pull ignores them. We don't
			// surface them in TartManifest because cove has no use for them.
		}
	}
	if !configFound {
		return out, fmt.Errorf("parse tart manifest: missing config layer (mediaType %s)", TartConfigMediaType)
	}
	if !nvramFound {
		return out, fmt.Errorf("parse tart manifest: missing nvram layer (mediaType %s)", TartNVRAMMediaType)
	}
	if len(out.DiskLayers) == 0 {
		return out, fmt.Errorf("parse tart manifest: no disk.v2 layers")
	}

	// Fill cumulative offsets and cross-check against the manifest-level
	// uncompressed-disk-size annotation when present.
	var offset int64
	for i := range out.DiskLayers {
		out.DiskLayers[i].Offset = offset
		offset += out.DiskLayers[i].UncompressedSize
	}
	if out.UncompressedDiskSize != 0 && out.UncompressedDiskSize != offset {
		return out, fmt.Errorf("parse tart manifest: layer bytes %d, want disk size %d", offset, out.UncompressedDiskSize)
	}
	if out.UncompressedDiskSize == 0 {
		out.UncompressedDiskSize = offset
	}
	return out, nil
}

func parseTartDiskLayer(layer Descriptor) (TartDiskLayer, error) {
	var out TartDiskLayer
	out.Descriptor = layer
	sizeStr, ok := layer.Annotations[TartUncompressedSize]
	if !ok {
		return out, fmt.Errorf("parse tart manifest: disk layer %s missing %s", layer.Digest, TartUncompressedSize)
	}
	size, err := strconv.ParseInt(sizeStr, 10, 64)
	if err != nil {
		return out, fmt.Errorf("parse tart manifest: disk layer %s invalid %s %q: %w", layer.Digest, TartUncompressedSize, sizeStr, err)
	}
	if size < 0 {
		return out, fmt.Errorf("parse tart manifest: disk layer %s negative uncompressed size %d", layer.Digest, size)
	}
	out.UncompressedSize = size
	digest, ok := layer.Annotations[TartUncompressedContentDigest]
	if !ok {
		return out, fmt.Errorf("parse tart manifest: disk layer %s missing %s", layer.Digest, TartUncompressedContentDigest)
	}
	if digest == "" {
		return out, fmt.Errorf("parse tart manifest: disk layer %s empty %s", layer.Digest, TartUncompressedContentDigest)
	}
	out.UncompressedContentDigest = digest
	return out, nil
}
