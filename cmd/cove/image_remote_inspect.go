package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/tmc/cove/internal/bytefmt"
	"github.com/tmc/cove/internal/imagestore"
	"github.com/tmc/cove/internal/ociimage"
)

const remoteInspectTimeout = 30 * time.Second
const remoteBaseChainLimit = 8

type remoteInspectOptions struct {
	RegistryBaseURL       string
	RegistryToken         string
	VerifyBlobs           bool
	Platform              string
	InspectIndexManifests bool
}

type ImageRemoteInspectOutput struct {
	Ref                 string                     `json:"ref"`
	Error               string                     `json:"error,omitempty"`
	ManifestDigest      string                     `json:"manifest_digest,omitempty"`
	ResolvedFromIndex   bool                       `json:"resolved_from_index,omitempty"`
	IndexDigest         string                     `json:"index_digest,omitempty"`
	IndexMediaType      string                     `json:"index_media_type,omitempty"`
	SelectedDigest      string                     `json:"selected_digest,omitempty"`
	SelectedPlatform    string                     `json:"selected_platform,omitempty"`
	IndexManifests      []ImageRemoteIndexManifest `json:"index_manifests,omitempty"`
	Kind                string                     `json:"kind"`
	Format              string                     `json:"format"`
	PullPlan            string                     `json:"pull_plan,omitempty"`
	Verification        string                     `json:"verification,omitempty"`
	BlobAudit           string                     `json:"blob_audit,omitempty"`
	BlobDescriptors     int                        `json:"blob_descriptors,omitempty"`
	BlobBytes           int64                      `json:"blob_bytes,omitempty"`
	MissingBlobs        []string                   `json:"missing_blobs,omitempty"`
	MediaType           string                     `json:"media_type,omitempty"`
	ConfigMediaType     string                     `json:"config_media_type,omitempty"`
	LayerCount          int                        `json:"layer_count"`
	TotalLayerBytes     int64                      `json:"total_layer_bytes"`
	DiskSize            int64                      `json:"disk_size,omitempty"`
	DiskFormat          string                     `json:"disk_format,omitempty"`
	DiskSHA256          string                     `json:"disk_sha256,omitempty"`
	CompressedDiskBytes int64                      `json:"compressed_disk_bytes,omitempty"`
	ChunkCount          int                        `json:"chunk_count,omitempty"`
	ZeroChunks          int                        `json:"zero_chunks,omitempty"`
	DiskLayerCount      int                        `json:"disk_layer_count,omitempty"`
	DiskPartCount       int                        `json:"disk_part_count,omitempty"`
	MetadataBlobs       int                        `json:"metadata_blobs,omitempty"`
	MetadataBytes       int64                      `json:"metadata_bytes,omitempty"`
	ConfigBytes         int64                      `json:"config_bytes,omitempty"`
	NVRAMBytes          int64                      `json:"nvram_bytes,omitempty"`
	BaseManifest        string                     `json:"base_manifest,omitempty"`
	BaseChainAudit      string                     `json:"base_chain_audit,omitempty"`
	BaseChainDepth      int                        `json:"base_chain_depth,omitempty"`
	BaseChain           []ImageRemoteBaseManifest  `json:"base_chain,omitempty"`
	UploadTime          string                     `json:"upload_time,omitempty"`
	ImageRef            string                     `json:"image_ref,omitempty"`
	ImageName           string                     `json:"image_name,omitempty"`
	ImageTag            string                     `json:"image_tag,omitempty"`
	Created             string                     `json:"created,omitempty"`
	BuiltAt             string                     `json:"built_at,omitempty"`
}

type ImageRemoteIndexManifest struct {
	Digest              string `json:"digest"`
	MediaType           string `json:"media_type,omitempty"`
	Size                int64  `json:"size,omitempty"`
	Platform            string `json:"platform,omitempty"`
	Selected            bool   `json:"selected,omitempty"`
	Kind                string `json:"kind,omitempty"`
	Format              string `json:"format,omitempty"`
	PullPlan            string `json:"pull_plan,omitempty"`
	DiskSize            int64  `json:"disk_size,omitempty"`
	DiskFormat          string `json:"disk_format,omitempty"`
	CompressedDiskBytes int64  `json:"compressed_disk_bytes,omitempty"`
	ChunkCount          int    `json:"chunk_count,omitempty"`
	ZeroChunks          int    `json:"zero_chunks,omitempty"`
	DiskLayerCount      int    `json:"disk_layer_count,omitempty"`
	DiskPartCount       int    `json:"disk_part_count,omitempty"`
	MetadataBlobs       int    `json:"metadata_blobs,omitempty"`
	MetadataBytes       int64  `json:"metadata_bytes,omitempty"`
	ConfigBytes         int64  `json:"config_bytes,omitempty"`
	NVRAMBytes          int64  `json:"nvram_bytes,omitempty"`
	BaseManifest        string `json:"base_manifest,omitempty"`
	Error               string `json:"error,omitempty"`
}

type ImageRemoteBaseManifest struct {
	Digest         string `json:"digest"`
	Status         string `json:"status"`
	Format         string `json:"format,omitempty"`
	DiskSize       int64  `json:"disk_size,omitempty"`
	DiskFormat     string `json:"disk_format,omitempty"`
	ChunkCount     int    `json:"chunk_count,omitempty"`
	MatchingChunks int    `json:"matching_chunks,omitempty"`
	MatchingBytes  int64  `json:"matching_bytes,omitempty"`
	BaseManifest   string `json:"base_manifest,omitempty"`
	Error          string `json:"error,omitempty"`
}

func InspectRemoteImages(ctx context.Context, refs []string, opts remoteInspectOptions) ([]ImageRemoteInspectOutput, error) {
	var firstErr error
	out := make([]ImageRemoteInspectOutput, 0, len(refs))
	for _, ref := range refs {
		result, err := InspectRemoteImage(ctx, ref, opts)
		if err != nil {
			result = ImageRemoteInspectOutput{Ref: ref, Error: err.Error()}
			if firstErr == nil {
				firstErr = err
			}
		}
		out = append(out, result)
	}
	if firstErr != nil {
		return out, fmt.Errorf("image inspect remote: %d of %d refs failed: %w", countRemoteInspectErrors(out), len(out), firstErr)
	}
	return out, nil
}

func InspectRemoteImage(ctx context.Context, refText string, opts remoteInspectOptions) (ImageRemoteInspectOutput, error) {
	ref, err := ociimage.ParseReference(refText)
	if err != nil {
		return ImageRemoteInspectOutput{}, fmt.Errorf("image inspect remote: invalid ref %q: %w", refText, err)
	}
	if ref.Tag == "" && ref.Digest == "" {
		return ImageRemoteInspectOutput{}, fmt.Errorf("image inspect remote: ref %q must include a tag or digest", refText)
	}
	client, err := remoteInspectRegistryClient(ref, opts)
	if err != nil {
		return ImageRemoteInspectOutput{}, err
	}
	manifest, resolution, err := client.FetchManifestInfo(ctx, ref)
	if err != nil {
		return ImageRemoteInspectOutput{}, err
	}
	out := remoteInspectBase(ref, resolution, manifest)
	if isCoveImageArtifactManifest(manifest) {
		out, err = inspectRemoteCoveImageArtifact(ctx, client, ref, manifest, out)
		if err != nil {
			return out, err
		}
		out = maybeInspectRemoteIndexManifests(ctx, client, ref, out, opts)
		return maybeAuditRemoteBlobs(ctx, client, ref, manifest, out, opts)
	}
	parsed, err := ociimage.ParseManifest(manifest)
	if err != nil {
		return ImageRemoteInspectOutput{}, fmt.Errorf("image inspect remote: parse manifest: %w", err)
	}
	out = inspectRemoteVMManifest(parsed, out)
	out = maybeAuditRemoteBaseChain(ctx, client, ref, parsed, out)
	out = maybeInspectRemoteIndexManifests(ctx, client, ref, out, opts)
	return maybeAuditRemoteBlobs(ctx, client, ref, manifest, out, opts)
}

func remoteInspectRegistryClient(ref ociimage.Reference, opts remoteInspectOptions) (ociimage.RegistryClient, error) {
	var platform *ociimage.Platform
	if opts.Platform != "" {
		p, err := ociimage.ParsePlatform(opts.Platform)
		if err != nil {
			return ociimage.RegistryClient{}, fmt.Errorf("image inspect remote: -platform: %w", err)
		}
		platform = &p
	}
	return ociimage.RegistryClient{
		BaseURL:       opts.RegistryBaseURL,
		Authorization: registryAuthorization(ref, opts.RegistryToken),
		TokenCache:    ociimage.NewRegistryTokenCache(),
		Platform:      platform,
	}, nil
}

func remoteInspectBase(ref ociimage.Reference, resolution ociimage.ManifestResolution, manifest ociimage.Manifest) ImageRemoteInspectOutput {
	var total int64
	for _, layer := range manifest.Layers {
		total += layer.Size
	}
	return ImageRemoteInspectOutput{
		Ref:               ref.String(),
		ManifestDigest:    resolution.Digest,
		ResolvedFromIndex: resolution.IndexDigest != "",
		IndexDigest:       resolution.IndexDigest,
		IndexMediaType:    resolution.IndexMediaType,
		SelectedDigest:    resolution.SelectedDigest,
		SelectedPlatform:  remotePlatformString(resolution.SelectedPlatform),
		IndexManifests:    remoteIndexManifestOutputs(resolution),
		Kind:              "vm-oci",
		MediaType:         manifest.MediaType,
		ConfigMediaType:   manifest.Config.MediaType,
		LayerCount:        len(manifest.Layers),
		TotalLayerBytes:   total,
	}
}

func remoteIndexManifestOutputs(resolution ociimage.ManifestResolution) []ImageRemoteIndexManifest {
	if len(resolution.IndexManifests) == 0 {
		return nil
	}
	out := make([]ImageRemoteIndexManifest, 0, len(resolution.IndexManifests))
	for _, desc := range resolution.IndexManifests {
		out = append(out, ImageRemoteIndexManifest{
			Digest:    desc.Digest,
			MediaType: desc.MediaType,
			Size:      desc.Size,
			Platform:  remotePlatformString(desc.Platform),
			Selected:  desc.Digest != "" && desc.Digest == resolution.SelectedDigest,
		})
	}
	return out
}

func maybeInspectRemoteIndexManifests(ctx context.Context, client ociimage.RegistryClient, ref ociimage.Reference, out ImageRemoteInspectOutput, opts remoteInspectOptions) ImageRemoteInspectOutput {
	if !opts.InspectIndexManifests || len(out.IndexManifests) == 0 {
		return out
	}
	for i := range out.IndexManifests {
		detail, err := inspectRemoteIndexManifest(ctx, client, ref, out.IndexManifests[i].Digest)
		if err != nil {
			out.IndexManifests[i].Error = err.Error()
			continue
		}
		out.IndexManifests[i].Kind = detail.Kind
		out.IndexManifests[i].Format = detail.Format
		out.IndexManifests[i].PullPlan = detail.PullPlan
		out.IndexManifests[i].DiskSize = detail.DiskSize
		out.IndexManifests[i].DiskFormat = detail.DiskFormat
		out.IndexManifests[i].CompressedDiskBytes = detail.CompressedDiskBytes
		out.IndexManifests[i].ChunkCount = detail.ChunkCount
		out.IndexManifests[i].ZeroChunks = detail.ZeroChunks
		out.IndexManifests[i].DiskLayerCount = detail.DiskLayerCount
		out.IndexManifests[i].DiskPartCount = detail.DiskPartCount
		out.IndexManifests[i].MetadataBlobs = detail.MetadataBlobs
		out.IndexManifests[i].MetadataBytes = detail.MetadataBytes
		out.IndexManifests[i].ConfigBytes = detail.ConfigBytes
		out.IndexManifests[i].NVRAMBytes = detail.NVRAMBytes
		out.IndexManifests[i].BaseManifest = detail.BaseManifest
	}
	return out
}

func inspectRemoteIndexManifest(ctx context.Context, client ociimage.RegistryClient, ref ociimage.Reference, digest string) (ImageRemoteIndexManifest, error) {
	childRef := ref
	childRef.Tag = ""
	childRef.Digest = digest
	manifest, _, err := client.FetchManifestInfo(ctx, childRef)
	if err != nil {
		return ImageRemoteIndexManifest{}, err
	}
	base := remoteInspectManifestBase(manifest)
	if isCoveImageArtifactManifest(manifest) {
		return remoteIndexManifestFromOutput(inspectRemoteCoveImageArtifactManifestOnly(manifest, base)), nil
	}
	parsed, err := ociimage.ParseManifest(manifest)
	if err != nil {
		return ImageRemoteIndexManifest{}, fmt.Errorf("parse manifest: %w", err)
	}
	return remoteIndexManifestFromOutput(inspectRemoteVMManifest(parsed, base)), nil
}

func remoteInspectManifestBase(manifest ociimage.Manifest) ImageRemoteInspectOutput {
	var total int64
	for _, layer := range manifest.Layers {
		total += layer.Size
	}
	return ImageRemoteInspectOutput{
		Kind:            "vm-oci",
		MediaType:       manifest.MediaType,
		ConfigMediaType: manifest.Config.MediaType,
		LayerCount:      len(manifest.Layers),
		TotalLayerBytes: total,
	}
}

func inspectRemoteCoveImageArtifactManifestOnly(manifest ociimage.Manifest, out ImageRemoteInspectOutput) ImageRemoteInspectOutput {
	out.Kind = "image-store"
	out.Format = "cove-image"
	out.PullPlan = "cove image-store artifact"
	out.ConfigBytes = manifest.Config.Size
	for _, layer := range manifest.Layers {
		title := layer.Annotations[ociTitleAnnotation]
		switch {
		case layer.MediaType == coveImageDiskType || title == "disk.img.gz":
			out.DiskLayerCount++
			out.CompressedDiskBytes += layer.Size
		case layer.MediaType == coveImageFileType:
			out.MetadataBlobs++
			out.MetadataBytes += layer.Size
		}
	}
	return out
}

func remoteIndexManifestFromOutput(out ImageRemoteInspectOutput) ImageRemoteIndexManifest {
	return ImageRemoteIndexManifest{
		Kind:                out.Kind,
		Format:              out.Format,
		PullPlan:            out.PullPlan,
		DiskSize:            out.DiskSize,
		DiskFormat:          out.DiskFormat,
		CompressedDiskBytes: out.CompressedDiskBytes,
		ChunkCount:          out.ChunkCount,
		ZeroChunks:          out.ZeroChunks,
		DiskLayerCount:      out.DiskLayerCount,
		DiskPartCount:       out.DiskPartCount,
		MetadataBlobs:       out.MetadataBlobs,
		MetadataBytes:       out.MetadataBytes,
		ConfigBytes:         out.ConfigBytes,
		NVRAMBytes:          out.NVRAMBytes,
		BaseManifest:        out.BaseManifest,
	}
}

func isCoveImageArtifactManifest(manifest ociimage.Manifest) bool {
	if manifest.Config.MediaType == coveImageConfigType {
		return true
	}
	for _, layer := range manifest.Layers {
		if layer.MediaType == coveImageDiskType || layer.MediaType == coveImageFileType {
			return true
		}
	}
	return false
}

func inspectRemoteCoveImageArtifact(ctx context.Context, client ociimage.RegistryClient, ref ociimage.Reference, manifest ociimage.Manifest, out ImageRemoteInspectOutput) (ImageRemoteInspectOutput, error) {
	out.Kind = "image-store"
	out.Format = "cove-image"
	out.PullPlan = "cove image-store artifact"
	out.Verification = "manifest parsed; image metadata blob size/digest verified"
	out.ConfigBytes = manifest.Config.Size
	if manifest.Config.Digest == "" {
		return out, fmt.Errorf("image inspect remote: cove image artifact missing config digest")
	}
	rc, err := client.FetchBlob(ctx, ref, manifest.Config.Digest)
	if err != nil {
		return out, err
	}
	configBytes, err := readRemoteInspectBlob(rc, manifest.Config, 1<<20)
	if err != nil {
		return out, fmt.Errorf("image inspect remote: fetch image metadata: %w", err)
	}
	var m imagestore.Manifest
	if err := json.Unmarshal(configBytes, &m); err != nil {
		return out, fmt.Errorf("image inspect remote: parse image metadata: %w", err)
	}
	out.ImageName = m.Name
	out.ImageTag = m.Tag
	if m.Name != "" || m.Tag != "" {
		out.ImageRef = m.Name + ":" + m.Tag
	}
	out.DiskSize = m.DiskSize
	out.DiskFormat = normalizeImageDiskFormat(m.DiskFormat)
	out.DiskSHA256 = m.DiskSHA256
	if !m.CreatedAt.IsZero() {
		out.Created = m.CreatedAt.UTC().Format(time.RFC3339)
	}
	if !m.BuiltAt.IsZero() {
		out.BuiltAt = m.BuiltAt.UTC().Format(time.RFC3339)
	}
	for _, layer := range manifest.Layers {
		title := layer.Annotations[ociTitleAnnotation]
		switch {
		case layer.MediaType == coveImageDiskType || title == "disk.img.gz":
			out.DiskLayerCount++
			out.CompressedDiskBytes += layer.Size
		case layer.MediaType == coveImageFileType:
			out.MetadataBlobs++
			out.MetadataBytes += layer.Size
		}
	}
	return out, nil
}

func readRemoteInspectBlob(rc io.ReadCloser, desc ociimage.Descriptor, limit int64) ([]byte, error) {
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("blob exceeds %d byte cap", limit)
	}
	if desc.Size >= 0 && int64(len(data)) != desc.Size {
		return nil, fmt.Errorf("size %d, want %d", len(data), desc.Size)
	}
	if desc.Digest != "" {
		if got := digestData(data); got != desc.Digest {
			return nil, fmt.Errorf("digest %s, want %s", got, desc.Digest)
		}
	}
	return data, nil
}

func inspectRemoteVMManifest(parsed ociimage.ParsedManifest, out ImageRemoteInspectOutput) ImageRemoteInspectOutput {
	out.Kind = "vm-oci"
	out.Format = parsed.Format.String()
	switch parsed.Format {
	case ociimage.FormatLume:
		out.PullPlan = "lume tar-split import"
		out.Verification = "manifest parsed; disk part size/digest verified during import"
		out.DiskPartCount = len(parsed.Lume.DiskParts)
		out.DiskLayerCount = len(parsed.Lume.DiskParts)
		for _, part := range parsed.Lume.DiskParts {
			out.CompressedDiskBytes += part.Descriptor.Size
		}
		if parsed.Lume.ConfigLayer != nil {
			out.ConfigBytes = parsed.Lume.ConfigLayer.Size
		}
		if parsed.Lume.NvramLayer != nil {
			out.NVRAMBytes = parsed.Lume.NvramLayer.Size
		}
	case ociimage.FormatTart:
		out.PullPlan = "tart-compatible import"
		out.Verification = "manifest parsed; sidecar digest and uncompressed disk digest verified during pull"
		out.DiskSize = parsed.Tart.UncompressedDiskSize
		out.DiskLayerCount = len(parsed.Tart.DiskLayers)
		out.ChunkCount = len(parsed.Tart.DiskLayers)
		for _, layer := range parsed.Tart.DiskLayers {
			out.CompressedDiskBytes += layer.Descriptor.Size
		}
		out.ConfigBytes = parsed.Tart.ConfigLayer.Size
		out.NVRAMBytes = parsed.Tart.NVRAMLayer.Size
		out.UploadTime = parsed.Tart.UploadTime
	default:
		out.Format = "cove"
		out.DiskSize = parsed.Annotations.UncompressedDiskSize
		out.DiskFormat = parsed.Annotations.DiskFormat
		out.BaseManifest = parsed.Annotations.BaseManifest
		out.PullPlan = "cove chunked pull"
		if out.BaseManifest != "" {
			out.PullPlan = "cove chunked pull with base reuse"
		}
		out.Verification = "manifest parsed; compressed and uncompressed chunk digests verified during pull"
		out.UploadTime = parsed.Annotations.UploadTime
		out.ChunkCount = len(parsed.Chunks)
		out.DiskLayerCount = len(parsed.DiskLayers)
		out.MetadataBlobs = len(parsed.Blobs)
		for _, layer := range parsed.DiskLayers {
			out.CompressedDiskBytes += layer.Descriptor.Size
		}
		for _, layer := range parsed.DiskLayers {
			if layer.Chunk.Zero {
				out.ZeroChunks++
			}
		}
		for _, blob := range parsed.Blobs {
			out.MetadataBytes += blob.Size
		}
	}
	return out
}

func maybeAuditRemoteBlobs(ctx context.Context, client ociimage.RegistryClient, ref ociimage.Reference, manifest ociimage.Manifest, out ImageRemoteInspectOutput, opts remoteInspectOptions) (ImageRemoteInspectOutput, error) {
	if !opts.VerifyBlobs {
		return out, nil
	}
	audit, err := auditRemoteBlobs(ctx, client, ref, manifest)
	if err != nil {
		return out, err
	}
	out.BlobAudit = audit.Status
	out.BlobDescriptors = audit.Checked
	out.BlobBytes = audit.Bytes
	out.MissingBlobs = audit.Missing
	return out, nil
}

func maybeAuditRemoteBaseChain(ctx context.Context, client ociimage.RegistryClient, ref ociimage.Reference, parsed ociimage.ParsedManifest, out ImageRemoteInspectOutput) ImageRemoteInspectOutput {
	if parsed.Format != ociimage.FormatCove || strings.TrimSpace(parsed.Annotations.BaseManifest) == "" {
		return out
	}
	audit := auditRemoteBaseChain(ctx, client, ref, parsed)
	out.BaseChainAudit = audit.Status
	out.BaseChainDepth = len(audit.Entries)
	out.BaseChain = audit.Entries
	return out
}

type remoteBaseChainAudit struct {
	Status  string
	Entries []ImageRemoteBaseManifest
}

func auditRemoteBaseChain(ctx context.Context, client ociimage.RegistryClient, ref ociimage.Reference, child ociimage.ParsedManifest) remoteBaseChainAudit {
	audit := remoteBaseChainAudit{Status: "ok"}
	seen := map[string]bool{}
	current := child
	for depth := 0; depth < remoteBaseChainLimit; depth++ {
		digest := strings.TrimSpace(current.Annotations.BaseManifest)
		if digest == "" {
			return audit
		}
		entry := ImageRemoteBaseManifest{Digest: digest, Status: "ok"}
		if seen[digest] {
			entry.Status = "cycle"
			entry.Error = "base manifest repeats earlier digest"
			audit.Entries = append(audit.Entries, entry)
			audit.Status = remoteBaseChainWorst(audit.Status, entry.Status)
			return audit
		}
		seen[digest] = true
		if !validSHA256Digest(digest) {
			entry.Status = "invalid"
			entry.Error = "base manifest digest is not sha256:<64 lowercase hex>"
			audit.Entries = append(audit.Entries, entry)
			audit.Status = remoteBaseChainWorst(audit.Status, entry.Status)
			return audit
		}
		baseManifest, err := fetchRemoteBaseManifest(ctx, client, ref, digest)
		if err != nil {
			entry.Status = remoteBaseFetchStatus(err)
			entry.Error = err.Error()
			audit.Entries = append(audit.Entries, entry)
			audit.Status = remoteBaseChainWorst(audit.Status, entry.Status)
			return audit
		}
		base, err := ociimage.ParseManifest(baseManifest)
		if err != nil {
			entry.Status = "invalid"
			entry.Error = err.Error()
			audit.Entries = append(audit.Entries, entry)
			audit.Status = remoteBaseChainWorst(audit.Status, entry.Status)
			return audit
		}
		entry.Format = base.Format.String()
		entry.DiskSize = base.Annotations.UncompressedDiskSize
		entry.DiskFormat = base.Annotations.DiskFormat
		entry.ChunkCount = len(base.Chunks)
		entry.BaseManifest = strings.TrimSpace(base.Annotations.BaseManifest)
		matching := matchingPullBaseChunks(current.DiskLayers, base.DiskLayers)
		entry.MatchingChunks = len(matching)
		entry.MatchingBytes = matchingPullBaseBytes(current.DiskLayers, matching)
		if base.Format != ociimage.FormatCove {
			entry.Status = "invalid"
			entry.Error = "base manifest is not cove format"
		} else if base.Annotations.UncompressedDiskSize != current.Annotations.UncompressedDiskSize {
			entry.Status = "incompatible"
			entry.Error = fmt.Sprintf("disk size %d, child %d", base.Annotations.UncompressedDiskSize, current.Annotations.UncompressedDiskSize)
		} else if base.Annotations.DiskFormat != current.Annotations.DiskFormat {
			entry.Status = "incompatible"
			entry.Error = fmt.Sprintf("disk format %s, child %s", base.Annotations.DiskFormat, current.Annotations.DiskFormat)
		} else if entry.MatchingChunks == 0 {
			entry.Status = "incompatible"
			entry.Error = "no reusable chunk descriptors match child"
		}
		audit.Entries = append(audit.Entries, entry)
		audit.Status = remoteBaseChainWorst(audit.Status, entry.Status)
		current = base
	}
	if next := strings.TrimSpace(current.Annotations.BaseManifest); next != "" {
		entry := ImageRemoteBaseManifest{
			Digest: next,
			Status: "depth-limit",
			Error:  fmt.Sprintf("base chain exceeds %d manifests", remoteBaseChainLimit),
		}
		audit.Entries = append(audit.Entries, entry)
		audit.Status = remoteBaseChainWorst(audit.Status, entry.Status)
	}
	return audit
}

func fetchRemoteBaseManifest(ctx context.Context, client ociimage.RegistryClient, ref ociimage.Reference, digest string) (ociimage.Manifest, error) {
	baseRef := ref
	baseRef.Tag = ""
	baseRef.Digest = digest
	manifest, _, err := client.FetchManifestInfo(ctx, baseRef)
	return manifest, err
}

func matchingPullBaseBytes(layers []ociimage.DiskLayer, matching map[int]bool) int64 {
	var bytes int64
	for _, layer := range layers {
		if matching[layer.Chunk.Index] {
			bytes += layer.Chunk.Size
		}
	}
	return bytes
}

func remoteBaseFetchStatus(err error) string {
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "404") || strings.Contains(text, "not found") {
		return "missing"
	}
	return "error"
}

func remoteBaseChainWorst(current, next string) string {
	if current == "" {
		return next
	}
	if remoteBaseChainRank(next) > remoteBaseChainRank(current) {
		return next
	}
	return current
}

func remoteBaseChainRank(status string) int {
	switch status {
	case "ok":
		return 0
	case "incompatible":
		return 1
	case "depth-limit":
		return 2
	case "cycle":
		return 3
	case "missing":
		return 4
	case "invalid":
		return 5
	case "error":
		return 6
	default:
		return 7
	}
}

type remoteBlobAudit struct {
	Status  string
	Checked int
	Bytes   int64
	Missing []string
}

func auditRemoteBlobs(ctx context.Context, client ociimage.RegistryClient, ref ociimage.Reference, manifest ociimage.Manifest) (remoteBlobAudit, error) {
	descriptors := remoteBlobDescriptors(manifest)
	audit := remoteBlobAudit{Status: "ok", Checked: len(descriptors)}
	for _, desc := range descriptors {
		if desc.Descriptor.Size > 0 {
			audit.Bytes += desc.Descriptor.Size
		}
		if desc.Descriptor.Digest == "" {
			audit.Missing = append(audit.Missing, desc.Name+":missing digest")
			continue
		}
		ok, err := client.BlobExists(ctx, ref, desc.Descriptor.Digest)
		if err != nil {
			return audit, fmt.Errorf("image inspect remote: verify blob %s: %w", desc.Name, err)
		}
		if !ok {
			audit.Missing = append(audit.Missing, desc.Name+":"+desc.Descriptor.Digest)
		}
	}
	if len(audit.Missing) > 0 {
		audit.Status = "missing"
	}
	return audit, nil
}

type remoteBlobDescriptor struct {
	Name       string
	Descriptor ociimage.Descriptor
}

func remoteBlobDescriptors(manifest ociimage.Manifest) []remoteBlobDescriptor {
	out := make([]remoteBlobDescriptor, 0, 1+len(manifest.Layers))
	out = append(out, remoteBlobDescriptor{Name: "config", Descriptor: manifest.Config})
	for i, layer := range manifest.Layers {
		name := fmt.Sprintf("layer[%d]", i)
		if title := layer.Annotations[ociTitleAnnotation]; title != "" {
			name = title
		} else if role := layer.Annotations[ociimage.CoveRole]; role != "" {
			name = role
		}
		out = append(out, remoteBlobDescriptor{Name: name, Descriptor: layer})
	}
	return out
}

func remotePlatformString(platform *ociimage.Platform) string {
	if platform == nil {
		return ""
	}
	var b strings.Builder
	if platform.OS != "" || platform.Architecture != "" {
		b.WriteString(platform.OS)
		if platform.OS != "" && platform.Architecture != "" {
			b.WriteByte('/')
		}
		b.WriteString(platform.Architecture)
	}
	if platform.Variant != "" {
		if b.Len() > 0 {
			b.WriteByte('/')
		}
		b.WriteString(platform.Variant)
	}
	if platform.OSVersion != "" {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString("os.version=")
		b.WriteString(platform.OSVersion)
	}
	if len(platform.Features) > 0 {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString("features=")
		b.WriteString(strings.Join(platform.Features, ","))
	}
	return b.String()
}

func countRemoteInspectErrors(out []ImageRemoteInspectOutput) int {
	var n int
	for _, result := range out {
		if result.Error != "" {
			n++
		}
	}
	return n
}

func writeRemoteInspectJSON(w io.Writer, out ImageRemoteInspectOutput) error {
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("encode remote inspect output: %w", err)
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

func writeRemoteInspectJSONList(w io.Writer, out []ImageRemoteInspectOutput) error {
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("encode remote inspect output: %w", err)
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

func writeRemoteInspectTextList(w io.Writer, out []ImageRemoteInspectOutput) error {
	for i, result := range out {
		if i > 0 {
			fmt.Fprintln(w)
		}
		if err := writeRemoteInspectText(w, result); err != nil {
			return err
		}
	}
	return nil
}

func writeRemoteInspectText(w io.Writer, out ImageRemoteInspectOutput) error {
	fmt.Fprintf(w, "Remote image %s\n", out.Ref)
	if out.Error != "" {
		fmt.Fprintf(w, "  error:           %s\n", out.Error)
		return nil
	}
	if out.ManifestDigest != "" {
		fmt.Fprintf(w, "  manifest digest: %s\n", out.ManifestDigest)
	}
	if out.ResolvedFromIndex {
		fmt.Fprintf(w, "  index digest:    %s\n", out.IndexDigest)
		if out.IndexMediaType != "" {
			fmt.Fprintf(w, "  index media:     %s\n", out.IndexMediaType)
		}
		if out.SelectedDigest != "" {
			fmt.Fprintf(w, "  selected digest: %s\n", out.SelectedDigest)
		}
		if out.SelectedPlatform != "" {
			fmt.Fprintf(w, "  platform:        %s\n", out.SelectedPlatform)
		}
		if len(out.IndexManifests) > 0 {
			fmt.Fprintf(w, "  index manifests: %d\n", len(out.IndexManifests))
			for _, manifest := range out.IndexManifests {
				marker := " "
				if manifest.Selected {
					marker = "*"
				}
				fmt.Fprintf(w, "    %s %s", marker, manifest.Digest)
				if manifest.Platform != "" {
					fmt.Fprintf(w, " platform=%s", manifest.Platform)
				}
				if manifest.Size > 0 {
					fmt.Fprintf(w, " size=%s", bytefmt.Size(manifest.Size))
				}
				if manifest.MediaType != "" {
					fmt.Fprintf(w, " media=%s", manifest.MediaType)
				}
				if manifest.Format != "" {
					fmt.Fprintf(w, " format=%s", manifest.Format)
				}
				if manifest.DiskFormat != "" {
					fmt.Fprintf(w, " disk_format=%s", manifest.DiskFormat)
				}
				if manifest.DiskSize > 0 {
					fmt.Fprintf(w, " disk_size=%s", bytefmt.Size(manifest.DiskSize))
				}
				if manifest.CompressedDiskBytes > 0 {
					fmt.Fprintf(w, " transport=%s", bytefmt.Size(manifest.CompressedDiskBytes))
				}
				if manifest.ChunkCount > 0 {
					fmt.Fprintf(w, " chunks=%d", manifest.ChunkCount)
				}
				if manifest.DiskPartCount > 0 {
					fmt.Fprintf(w, " parts=%d", manifest.DiskPartCount)
				}
				if manifest.Error != "" {
					fmt.Fprintf(w, " error=%q", manifest.Error)
				}
				fmt.Fprintln(w)
			}
		}
	}
	fmt.Fprintf(w, "  kind:            %s\n", out.Kind)
	fmt.Fprintf(w, "  format:          %s\n", out.Format)
	if out.PullPlan != "" {
		fmt.Fprintf(w, "  pull plan:       %s\n", out.PullPlan)
	}
	if out.Verification != "" {
		fmt.Fprintf(w, "  verification:    %s\n", out.Verification)
	}
	if out.BlobAudit != "" {
		fmt.Fprintf(w, "  blob audit:      %s", out.BlobAudit)
		if out.BlobDescriptors > 0 {
			fmt.Fprintf(w, " (%d descriptors", out.BlobDescriptors)
			if out.BlobBytes > 0 {
				fmt.Fprintf(w, ", %s)", bytefmt.Size(out.BlobBytes))
			} else {
				fmt.Fprint(w, ")")
			}
		}
		fmt.Fprintln(w)
		for _, missing := range out.MissingBlobs {
			fmt.Fprintf(w, "    missing:       %s\n", missing)
		}
	}
	if out.ImageRef != "" {
		fmt.Fprintf(w, "  image ref:       %s\n", out.ImageRef)
	}
	if out.DiskSize > 0 {
		fmt.Fprintf(w, "  disk size:       %s\n", bytefmt.Size(out.DiskSize))
	}
	if out.DiskFormat != "" {
		fmt.Fprintf(w, "  disk format:     %s\n", out.DiskFormat)
	}
	if out.DiskSHA256 != "" {
		fmt.Fprintf(w, "  disk sha256:     %s\n", out.DiskSHA256)
	}
	if out.CompressedDiskBytes > 0 {
		fmt.Fprintf(w, "  disk transport:  %s\n", bytefmt.Size(out.CompressedDiskBytes))
	}
	if out.ChunkCount > 0 {
		if out.ZeroChunks > 0 {
			fmt.Fprintf(w, "  chunks:          %d (%d zero)\n", out.ChunkCount, out.ZeroChunks)
		} else {
			fmt.Fprintf(w, "  chunks:          %d\n", out.ChunkCount)
		}
	}
	if out.DiskPartCount > 0 {
		fmt.Fprintf(w, "  disk parts:      %d\n", out.DiskPartCount)
	}
	if out.MetadataBlobs > 0 {
		fmt.Fprintf(w, "  metadata blobs:  %d", out.MetadataBlobs)
		if out.MetadataBytes > 0 {
			fmt.Fprintf(w, " (%s)", bytefmt.Size(out.MetadataBytes))
		}
		fmt.Fprintln(w)
	}
	if out.ConfigBytes > 0 {
		fmt.Fprintf(w, "  config:          %s\n", bytefmt.Size(out.ConfigBytes))
	}
	if out.NVRAMBytes > 0 {
		fmt.Fprintf(w, "  nvram:           %s\n", bytefmt.Size(out.NVRAMBytes))
	}
	if out.LayerCount > 0 {
		fmt.Fprintf(w, "  layers:          %d", out.LayerCount)
		if out.TotalLayerBytes > 0 {
			fmt.Fprintf(w, " (%s)", bytefmt.Size(out.TotalLayerBytes))
		}
		fmt.Fprintln(w)
	}
	if out.BaseManifest != "" {
		fmt.Fprintf(w, "  base manifest:   %s\n", out.BaseManifest)
	}
	if out.BaseChainAudit != "" {
		fmt.Fprintf(w, "  base audit:      %s", out.BaseChainAudit)
		if out.BaseChainDepth > 0 {
			fmt.Fprintf(w, " (%d manifests)", out.BaseChainDepth)
		}
		fmt.Fprintln(w)
		for _, entry := range out.BaseChain {
			fmt.Fprintf(w, "    base:          %s %s", entry.Digest, entry.Status)
			if entry.Format != "" {
				fmt.Fprintf(w, " format=%s", entry.Format)
			}
			if entry.DiskFormat != "" {
				fmt.Fprintf(w, " disk_format=%s", entry.DiskFormat)
			}
			if entry.MatchingChunks > 0 {
				fmt.Fprintf(w, " matching_chunks=%d", entry.MatchingChunks)
			}
			if entry.MatchingBytes > 0 {
				fmt.Fprintf(w, " matching_bytes=%s", bytefmt.Size(entry.MatchingBytes))
			}
			if entry.BaseManifest != "" {
				fmt.Fprintf(w, " parent=%s", entry.BaseManifest)
			}
			if entry.Error != "" {
				fmt.Fprintf(w, " error=%s", entry.Error)
			}
			fmt.Fprintln(w)
		}
	}
	if out.UploadTime != "" {
		fmt.Fprintf(w, "  upload time:     %s\n", out.UploadTime)
	}
	if out.Created != "" {
		fmt.Fprintf(w, "  created:         %s\n", out.Created)
	}
	if out.BuiltAt != "" {
		fmt.Fprintf(w, "  built at:        %s\n", out.BuiltAt)
	}
	if out.MediaType != "" || out.ConfigMediaType != "" {
		fmt.Fprintf(w, "  media:           %s", out.MediaType)
		if out.ConfigMediaType != "" {
			fmt.Fprintf(w, " / %s", out.ConfigMediaType)
		}
		fmt.Fprintln(w)
	}
	return nil
}
