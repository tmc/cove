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

type remoteInspectOptions struct {
	RegistryBaseURL string
	RegistryToken   string
}

type ImageRemoteInspectOutput struct {
	Ref                 string `json:"ref"`
	Error               string `json:"error,omitempty"`
	ManifestDigest      string `json:"manifest_digest,omitempty"`
	ResolvedFromIndex   bool   `json:"resolved_from_index,omitempty"`
	IndexDigest         string `json:"index_digest,omitempty"`
	IndexMediaType      string `json:"index_media_type,omitempty"`
	SelectedDigest      string `json:"selected_digest,omitempty"`
	SelectedPlatform    string `json:"selected_platform,omitempty"`
	Kind                string `json:"kind"`
	Format              string `json:"format"`
	PullPlan            string `json:"pull_plan,omitempty"`
	Verification        string `json:"verification,omitempty"`
	MediaType           string `json:"media_type,omitempty"`
	ConfigMediaType     string `json:"config_media_type,omitempty"`
	LayerCount          int    `json:"layer_count"`
	TotalLayerBytes     int64  `json:"total_layer_bytes"`
	DiskSize            int64  `json:"disk_size,omitempty"`
	DiskSHA256          string `json:"disk_sha256,omitempty"`
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
	UploadTime          string `json:"upload_time,omitempty"`
	ImageRef            string `json:"image_ref,omitempty"`
	ImageName           string `json:"image_name,omitempty"`
	ImageTag            string `json:"image_tag,omitempty"`
	Created             string `json:"created,omitempty"`
	BuiltAt             string `json:"built_at,omitempty"`
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
	client := pullRegistryClient(ref, pullOptions{
		RegistryBaseURL: opts.RegistryBaseURL,
		RegistryToken:   opts.RegistryToken,
	})
	manifest, resolution, err := client.FetchManifestInfo(ctx, ref)
	if err != nil {
		return ImageRemoteInspectOutput{}, err
	}
	out := remoteInspectBase(ref, resolution, manifest)
	if isCoveImageArtifactManifest(manifest) {
		return inspectRemoteCoveImageArtifact(ctx, client, ref, manifest, out)
	}
	parsed, err := ociimage.ParseManifest(manifest)
	if err != nil {
		return ImageRemoteInspectOutput{}, fmt.Errorf("image inspect remote: parse manifest: %w", err)
	}
	return inspectRemoteVMManifest(parsed, out), nil
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
		Kind:              "vm-oci",
		MediaType:         manifest.MediaType,
		ConfigMediaType:   manifest.Config.MediaType,
		LayerCount:        len(manifest.Layers),
		TotalLayerBytes:   total,
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
	}
	fmt.Fprintf(w, "  kind:            %s\n", out.Kind)
	fmt.Fprintf(w, "  format:          %s\n", out.Format)
	if out.PullPlan != "" {
		fmt.Fprintf(w, "  pull plan:       %s\n", out.PullPlan)
	}
	if out.Verification != "" {
		fmt.Fprintf(w, "  verification:    %s\n", out.Verification)
	}
	if out.ImageRef != "" {
		fmt.Fprintf(w, "  image ref:       %s\n", out.ImageRef)
	}
	if out.DiskSize > 0 {
		fmt.Fprintf(w, "  disk size:       %s\n", bytefmt.Size(out.DiskSize))
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
