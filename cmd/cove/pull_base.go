package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tmc/cove/internal/ociimage"
	"github.com/tmc/cove/internal/store"
	"github.com/tmc/cove/internal/vmconfig"
)

const pullBaseProbeTimeout = 50 * time.Millisecond

type pullBaseReuse struct {
	DiskPath       string
	DiskFormat     string
	MatchingChunks map[int]bool
}

func planPullBaseReuse(plan *pullPlan, blobStore store.Store) (*pullBaseReuse, error) {
	if plan == nil || plan.Manifest.Format != ociimage.FormatCove {
		return nil, nil
	}
	baseDigest := strings.TrimSpace(plan.Manifest.Annotations.BaseManifest)
	if baseDigest == "" {
		return nil, nil
	}
	baseManifest, ok, err := blobStore.LoadManifest(baseDigest)
	if err != nil || !ok {
		return nil, nil
	}
	parsedBase, err := ociimage.ParseManifest(baseManifest)
	if err != nil {
		return nil, nil
	}
	if parsedBase.Format != ociimage.FormatCove {
		return nil, nil
	}
	if parsedBase.Annotations.UncompressedDiskSize != plan.Manifest.Annotations.UncompressedDiskSize {
		return nil, nil
	}
	if parsedBase.Annotations.DiskFormat != plan.Manifest.Annotations.DiskFormat {
		return nil, nil
	}
	matching := matchingPullBaseChunks(plan.Manifest.DiskLayers, parsedBase.DiskLayers)
	if len(matching) == 0 {
		return nil, nil
	}
	diskPath, ok, err := findPullBaseDiskInRoots([]string{
		vmconfig.BaseDir(),
		buildRegistryBaseCacheRoot(blobStore.Dir),
	}, baseDigest, parsedBase.Annotations.UncompressedDiskSize, parsedBase.Annotations.DiskFormat, plan.VMDir)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return &pullBaseReuse{
		DiskPath:       diskPath,
		DiskFormat:     parsedBase.Annotations.DiskFormat,
		MatchingChunks: matching,
	}, nil
}

func planPullDryRunReuse(plan *pullPlan, opts pullOptions) error {
	if plan == nil || !opts.DryRun || plan.Manifest.Format != ociimage.FormatCove || len(plan.Manifest.DiskLayers) == 0 {
		return nil
	}
	blobStore := store.New(opts.StoreDir)
	baseReuse, err := planPullBaseReuse(plan, blobStore)
	if err != nil {
		return err
	}
	recordPullBaseReuse(plan, baseReuse)
	return recordPullDryRunTransfer(plan, blobStore, baseReuse)
}

func recordPullBaseReuse(plan *pullPlan, baseReuse *pullBaseReuse) {
	if plan == nil || baseReuse == nil || len(baseReuse.MatchingChunks) == 0 {
		return
	}
	plan.BaseReusePath = baseReuse.DiskPath
	plan.BaseReuseDiskFormat = baseReuse.DiskFormat
	plan.BaseReuseChunks = len(baseReuse.MatchingChunks)
	plan.BaseReuseBytes = matchingPullBaseBytes(plan.Manifest.DiskLayers, baseReuse.MatchingChunks)
}

func recordPullDryRunTransfer(plan *pullPlan, blobStore store.Store, baseReuse *pullBaseReuse) error {
	for _, layer := range plan.Manifest.DiskLayers {
		if baseReuse != nil && baseReuse.MatchingChunks[layer.Chunk.Index] {
			continue
		}
		if layer.Chunk.Zero {
			plan.ZeroDiskChunks++
			plan.ZeroDiskBytes += layer.Chunk.Size
			continue
		}
		if layer.Descriptor.Size < 0 {
			return fmt.Errorf("pull disk chunk %d: negative blob size %d", layer.Chunk.Index, layer.Descriptor.Size)
		}
		if blobStore.VerifyBlob(layer.Descriptor.Digest, layer.Descriptor.Size) == nil {
			plan.StoreDiskChunks++
			plan.StoreDiskBytes += layer.Descriptor.Size
			continue
		}
		plan.FetchDiskChunks++
		plan.FetchDiskBytes += layer.Descriptor.Size
		plan.FetchBlobDescriptors = append(plan.FetchBlobDescriptors, pullBlobDescriptor{
			Name:       fmt.Sprintf("chunk[%d]", layer.Chunk.Index),
			Descriptor: layer.Descriptor,
		})
	}
	for _, desc := range plan.Manifest.Blobs {
		name, ok, err := pullMetadataFileName(desc)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		if desc.Digest == "" {
			return fmt.Errorf("pull metadata blob: missing digest")
		}
		if desc.Size < 0 {
			return fmt.Errorf("pull metadata blob: negative size %d", desc.Size)
		}
		if blobStore.VerifyBlob(desc.Digest, desc.Size) == nil {
			plan.StoreMetadataBlobs++
			plan.StoreMetadataBytes += desc.Size
			continue
		}
		plan.FetchMetadataBlobs++
		plan.FetchMetadataBytes += desc.Size
		plan.FetchBlobDescriptors = append(plan.FetchBlobDescriptors, pullBlobDescriptor{
			Name:       name,
			Descriptor: desc,
		})
	}
	return nil
}

func findPullBaseDiskInRoots(roots []string, digest string, size int64, diskFormat string, targetDir string) (string, bool, error) {
	for _, root := range roots {
		diskPath, ok, err := findPullBaseDisk(root, digest, size, diskFormat, targetDir)
		if err != nil || ok {
			return diskPath, ok, err
		}
	}
	return "", false, nil
}

func matchingPullBaseChunks(target, base []ociimage.DiskLayer) map[int]bool {
	if len(target) != len(base) {
		return nil
	}
	matching := map[int]bool{}
	for i := range target {
		if !samePullBaseLayer(target[i], base[i]) {
			continue
		}
		matching[target[i].Chunk.Index] = true
	}
	return matching
}

func samePullBaseLayer(a, b ociimage.DiskLayer) bool {
	return a.Chunk.Index == b.Chunk.Index &&
		a.Chunk.Offset == b.Chunk.Offset &&
		a.Chunk.Size == b.Chunk.Size &&
		a.Chunk.Digest == b.Chunk.Digest &&
		a.Chunk.Zero == b.Chunk.Zero &&
		a.Descriptor.MediaType == b.Descriptor.MediaType &&
		a.Descriptor.Size == b.Descriptor.Size &&
		a.Descriptor.Digest == b.Descriptor.Digest
}

func findPullBaseDisk(root, digest string, size int64, diskFormat string, targetDir string) (string, bool, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		if samePullPath(dir, targetDir) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, "disk.provenance"))
		if err != nil || strings.TrimSpace(string(data)) != digest {
			continue
		}
		diskPath := filepath.Join(dir, "disk.img")
		info, err := os.Stat(diskPath)
		if err != nil || !info.Mode().IsRegular() || info.Size() != size {
			continue
		}
		if diskFormat != "" && detectImageDiskFormat(diskPath) != diskFormat {
			continue
		}
		active, err := probeControlSocket(GetControlSocketPathForVM(dir), pullBaseProbeTimeout)
		if err != nil || active {
			continue
		}
		return diskPath, true, nil
	}
	return "", false, nil
}

func samePullPath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return resolvePath(a) == resolvePath(b)
}

func createPullPartialDisk(path string, size int64, baseReuse *pullBaseReuse, resume bool) (*os.File, *pullBaseReuse, bool, error) {
	if resume {
		f, err := os.OpenFile(path, os.O_RDWR, 0600)
		if err == nil {
			if err := f.Truncate(size); err != nil {
				f.Close()
				return nil, nil, false, fmt.Errorf("size partial disk: %w", err)
			}
			return f, nil, true, nil
		}
		if !os.IsNotExist(err) {
			return nil, nil, false, fmt.Errorf("open partial disk: %w", err)
		}
	}
	if baseReuse == nil {
		f, err := ociimage.CreatePartialDisk(path, size)
		return f, nil, false, err
	}
	if err := cloneFile(baseReuse.DiskPath, path); err != nil {
		f, createErr := ociimage.CreatePartialDisk(path, size)
		return f, nil, false, createErr
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		os.Remove(path)
		return nil, nil, false, fmt.Errorf("open cloned partial disk: %w", err)
	}
	if err := f.Truncate(size); err != nil {
		f.Close()
		os.Remove(path)
		return nil, nil, false, fmt.Errorf("size cloned partial disk: %w", err)
	}
	return f, baseReuse, false, nil
}

func pullDiskChunkWork(layers []ociimage.DiskLayer, baseReuse *pullBaseReuse, zeroExisting bool) ([]ociimage.DiskLayer, []ociimage.DiskLayer) {
	fetchLayers := make([]ociimage.DiskLayer, 0, len(layers))
	var zeroLayers []ociimage.DiskLayer
	for _, layer := range layers {
		if baseReuse != nil && baseReuse.MatchingChunks[layer.Chunk.Index] {
			continue
		}
		if layer.Chunk.Zero {
			if baseReuse != nil || zeroExisting {
				zeroLayers = append(zeroLayers, layer)
			}
			continue
		}
		fetchLayers = append(fetchLayers, layer)
	}
	return fetchLayers, zeroLayers
}

func zeroPullDiskChunks(w io.WriterAt, layers []ociimage.DiskLayer) error {
	for _, layer := range layers {
		if err := writeZeroPullChunk(w, layer.Chunk); err != nil {
			return err
		}
	}
	return nil
}

func writeZeroPullChunk(w io.WriterAt, chunk ociimage.Chunk) error {
	if chunk.Size < 0 {
		return fmt.Errorf("zero chunk %d: negative size %d", chunk.Index, chunk.Size)
	}
	const blockSize = 1024 * 1024
	zero := make([]byte, blockSize)
	remaining := chunk.Size
	offset := chunk.Offset
	for remaining > 0 {
		n := int64(len(zero))
		if remaining < n {
			n = remaining
		}
		wrote, err := w.WriteAt(zero[:n], offset)
		if err != nil {
			return fmt.Errorf("zero chunk %d: %w", chunk.Index, err)
		}
		if int64(wrote) != n {
			return fmt.Errorf("zero chunk %d: short write %d, want %d", chunk.Index, wrote, n)
		}
		offset += n
		remaining -= n
	}
	return nil
}
