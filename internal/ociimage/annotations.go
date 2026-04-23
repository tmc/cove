package ociimage

import (
	"fmt"
	"strconv"
)

const (
	// CoveUncompressedSize records the uncompressed layer size in bytes.
	CoveUncompressedSize          = "org.tmc.cove.uncompressed-size"
	CoveUncompressedContentDigest = "org.tmc.cove.uncompressed-content-digest"
	CoveChunkIndex                = "org.tmc.cove.chunk-index"
	CoveChunkTotal                = "org.tmc.cove.chunk-total"
	CoveRole                      = "org.tmc.cove.role"
	CoveUploadTime                = "org.tmc.cove.upload-time"
	CoveUncompressedDiskSize      = "org.tmc.cove.uncompressed-disk-size"
	CoveHWModelDigest             = "org.tmc.cove.hw-model-digest"
	CoveAuxDigest                 = "org.tmc.cove.aux-digest"
	CoveBaseManifest              = "org.tmc.cove.base-manifest"

	// LumeUncompressedSize records the legacy lume uncompressed layer size.
	LumeUncompressedSize          = "org.trycua.lume.uncompressed-size"
	LumeUncompressedContentDigest = "org.trycua.lume.uncompressed-content-digest"
	LumeChunkIndex                = "org.trycua.lume.chunk-index"
	LumeChunkTotal                = "org.trycua.lume.chunk-total"
	LumeRole                      = "org.trycua.lume.role"
	LumeUploadTime                = "org.trycua.lume.upload-time"
	LumeUncompressedDiskSize      = "org.trycua.lume.uncompressed-disk-size"
)

var coveToLume = map[string]string{
	CoveUncompressedSize:          LumeUncompressedSize,
	CoveUncompressedContentDigest: LumeUncompressedContentDigest,
	CoveChunkIndex:                LumeChunkIndex,
	CoveChunkTotal:                LumeChunkTotal,
	CoveRole:                      LumeRole,
	CoveUploadTime:                LumeUploadTime,
	CoveUncompressedDiskSize:      LumeUncompressedDiskSize,
}

var lumeToCove = map[string]string{
	LumeUncompressedSize:          CoveUncompressedSize,
	LumeUncompressedContentDigest: CoveUncompressedContentDigest,
	LumeChunkIndex:                CoveChunkIndex,
	LumeChunkTotal:                CoveChunkTotal,
	LumeRole:                      CoveRole,
	LumeUploadTime:                CoveUploadTime,
	LumeUncompressedDiskSize:      CoveUncompressedDiskSize,
}

// ManifestAnnotations is the normalized annotation set stored on an OCI manifest.
type ManifestAnnotations struct {
	UploadTime           string
	UncompressedDiskSize int64
	HWModelDigest        string
	AuxDigest            string
	BaseManifest         string
}

// LayerAnnotations is the normalized annotation set stored on an OCI layer.
type LayerAnnotations struct {
	Role                      string
	UncompressedSize          int64
	UncompressedContentDigest string
	ChunkIndex                int
	ChunkTotal                int
}

// NormalizeManifestAnnotations reads cove annotations, accepting legacy Lume
// names where cove names are absent.
func NormalizeManifestAnnotations(in map[string]string) (ManifestAnnotations, error) {
	var out ManifestAnnotations
	diskSize, ok := annotationValue(in, CoveUncompressedDiskSize)
	if !ok {
		return out, missingAnnotationError(CoveUncompressedDiskSize)
	}
	size, err := parseIntAnnotation(CoveUncompressedDiskSize, diskSize)
	if err != nil {
		return out, err
	}
	out.UncompressedDiskSize = size
	out.UploadTime, _ = annotationValue(in, CoveUploadTime)
	out.HWModelDigest, _ = annotationValue(in, CoveHWModelDigest)
	out.AuxDigest, _ = annotationValue(in, CoveAuxDigest)
	out.BaseManifest, _ = annotationValue(in, CoveBaseManifest)
	return out, nil
}

// NormalizeLayerAnnotations reads cove layer annotations, accepting legacy Lume
// names where cove names are absent.
func NormalizeLayerAnnotations(in map[string]string) (LayerAnnotations, error) {
	var out LayerAnnotations
	out.Role, _ = annotationValue(in, CoveRole)

	if !hasAnyAnnotation(in,
		CoveUncompressedSize,
		CoveUncompressedContentDigest,
		CoveChunkIndex,
		CoveChunkTotal,
	) {
		return out, nil
	}

	sizeText, ok := annotationValue(in, CoveUncompressedSize)
	if !ok {
		return out, missingAnnotationError(CoveUncompressedSize)
	}
	digest, ok := annotationValue(in, CoveUncompressedContentDigest)
	if !ok {
		return out, missingAnnotationError(CoveUncompressedContentDigest)
	}
	indexText, ok := annotationValue(in, CoveChunkIndex)
	if !ok {
		return out, missingAnnotationError(CoveChunkIndex)
	}
	totalText, ok := annotationValue(in, CoveChunkTotal)
	if !ok {
		return out, missingAnnotationError(CoveChunkTotal)
	}

	size, err := parseIntAnnotation(CoveUncompressedSize, sizeText)
	if err != nil {
		return out, err
	}
	index, err := parseIntAnnotation(CoveChunkIndex, indexText)
	if err != nil {
		return out, err
	}
	total, err := parseIntAnnotation(CoveChunkTotal, totalText)
	if err != nil {
		return out, err
	}

	out.UncompressedSize = size
	out.UncompressedContentDigest = digest
	out.ChunkIndex = int(index)
	out.ChunkTotal = int(total)
	return out, nil
}

// AddLumeCompatibility copies cove annotations to their legacy Lume names.
func AddLumeCompatibility(in map[string]string) map[string]string {
	out := make(map[string]string, len(in)+len(coveToLume))
	for k, v := range in {
		out[k] = v
	}
	for coveKey, lumeKey := range coveToLume {
		v, ok := out[coveKey]
		if !ok {
			continue
		}
		if _, exists := out[lumeKey]; !exists {
			out[lumeKey] = v
		}
	}
	return out
}

func annotationValue(in map[string]string, coveKey string) (string, bool) {
	if v, ok := in[coveKey]; ok {
		return v, true
	}
	lumeKey, ok := coveToLume[coveKey]
	if !ok {
		return "", false
	}
	v, ok := in[lumeKey]
	return v, ok
}

func hasAnyAnnotation(in map[string]string, keys ...string) bool {
	for _, coveKey := range keys {
		if _, ok := annotationValue(in, coveKey); ok {
			return true
		}
	}
	return false
}

func parseIntAnnotation(key, value string) (int64, error) {
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse annotation %s: %w", key, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("parse annotation %s: negative value %d", key, n)
	}
	return n, nil
}

func missingAnnotationError(key string) error {
	if lumeKey, ok := coveToLume[key]; ok {
		return fmt.Errorf("missing annotation %s or %s", key, lumeKey)
	}
	return fmt.Errorf("missing annotation %s", key)
}

// NormalizeAnnotationKeys returns a copy with legacy Lume keys rewritten to
// cove keys. If both are present, the cove key wins.
func NormalizeAnnotationKeys(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		if coveKey, ok := lumeToCove[k]; ok {
			if _, exists := out[coveKey]; !exists {
				out[coveKey] = v
			}
			continue
		}
		out[k] = v
	}
	for k, v := range in {
		if _, ok := coveToLume[k]; ok {
			out[k] = v
		}
	}
	return out
}
