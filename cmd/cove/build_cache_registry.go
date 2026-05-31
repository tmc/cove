package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/tmc/cove/internal/ociimage"
	"github.com/tmc/cove/internal/store"
)

const (
	buildRegistryCacheVersion         = 1
	buildRegistryCacheConfigMediaType = "application/vnd.tmc.cove.build-cache.v1+json"
	buildRegistryCacheBlockMediaType  = "application/vnd.tmc.cove.build-cache.block.v1"
)

type buildRegistryCachePayload struct {
	Version   int                  `json:"version"`
	CreatedAt time.Time            `json:"created_at"`
	Entries   []buildCacheEntry    `json:"entries"`
	Layers    []buildLayerManifest `json:"layers"`
}

func importBuildRegistryCaches(ctx context.Context, opts buildOptions, s store.Store) error {
	for _, ref := range opts.CacheFrom {
		if err := importBuildRegistryCache(ctx, ref, opts, s); err != nil {
			return err
		}
	}
	return nil
}

func importBuildRegistryCache(ctx context.Context, refText string, opts buildOptions, s store.Store) error {
	ref, err := ociimage.ParseReference(refText)
	if err != nil {
		return fmt.Errorf("cove build: parse --cache-from: %w", err)
	}
	client := buildRegistryCacheClient(ref, opts)
	manifest, _, err := client.FetchManifest(ctx, ref)
	if err != nil {
		return fmt.Errorf("cove build: fetch cache %s: %w", refText, err)
	}
	if manifest.Config.MediaType != buildRegistryCacheConfigMediaType {
		return fmt.Errorf("cove build: cache %s config media type %q, want %q", refText, manifest.Config.MediaType, buildRegistryCacheConfigMediaType)
	}
	payload, err := fetchBuildRegistryCachePayload(ctx, client, ref, manifest.Config)
	if err != nil {
		return fmt.Errorf("cove build: fetch cache %s config: %w", refText, err)
	}
	if err := validateBuildRegistryCachePayload(payload, manifest); err != nil {
		return fmt.Errorf("cove build: cache %s: %w", refText, err)
	}
	if err := fetchBuildRegistryCacheBlocks(ctx, client, ref, s, payload); err != nil {
		return fmt.Errorf("cove build: fetch cache %s blocks: %w", refText, err)
	}
	for _, layer := range payload.Layers {
		if err := saveBuildLayerManifest(s, layer); err != nil {
			return fmt.Errorf("cove build: import cache layer %s: %w", layer.Digest, err)
		}
	}
	for _, entry := range payload.Entries {
		if err := saveBuildCacheEntry(s, entry); err != nil {
			return fmt.Errorf("cove build: import cache key %s: %w", entry.Key, err)
		}
	}
	return nil
}

func fetchBuildRegistryCachePayload(ctx context.Context, client ociimage.RegistryClient, ref ociimage.Reference, desc ociimage.Descriptor) (buildRegistryCachePayload, error) {
	var payload buildRegistryCachePayload
	body, err := client.FetchBlob(ctx, ref, desc.Digest)
	if err != nil {
		return payload, err
	}
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		return payload, err
	}
	if int64(len(data)) != desc.Size {
		return payload, fmt.Errorf("size %d, want %d", len(data), desc.Size)
	}
	if got := digestBytes(data); got != desc.Digest {
		return payload, fmt.Errorf("digest %s, want %s", got, desc.Digest)
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return payload, err
	}
	return payload, nil
}

func validateBuildRegistryCachePayload(payload buildRegistryCachePayload, manifest ociimage.Manifest) error {
	if payload.Version != buildRegistryCacheVersion {
		return fmt.Errorf("version %d, want %d", payload.Version, buildRegistryCacheVersion)
	}
	layers := map[string]bool{}
	for _, layer := range payload.Layers {
		if err := validateBuildLayerManifest(layer); err != nil {
			return fmt.Errorf("layer %s: %w", layer.Digest, err)
		}
		layers[layer.Digest] = true
	}
	for _, entry := range payload.Entries {
		if err := validateBuildCacheEntry(entry); err != nil {
			return fmt.Errorf("entry %s: %w", entry.Key, err)
		}
		if !layers[entry.LayerDigest] {
			return fmt.Errorf("entry %s references missing layer %s", entry.Key, entry.LayerDigest)
		}
	}
	descs := map[string]ociimage.Descriptor{}
	for _, desc := range manifest.Layers {
		if desc.MediaType != buildRegistryCacheBlockMediaType {
			continue
		}
		descs[desc.Digest] = desc
	}
	for _, layer := range payload.Layers {
		for _, block := range layer.Blocks {
			desc, ok := descs[block.Digest]
			if !ok {
				return fmt.Errorf("layer %s block %s missing from OCI layers", layer.Digest, block.Digest)
			}
			if desc.Size != block.Size {
				return fmt.Errorf("layer %s block %s size %d, want %d", layer.Digest, block.Digest, desc.Size, block.Size)
			}
		}
	}
	return nil
}

func fetchBuildRegistryCacheBlocks(ctx context.Context, client ociimage.RegistryClient, ref ociimage.Reference, s store.Store, payload buildRegistryCachePayload) error {
	seen := map[string]int64{}
	for _, layer := range payload.Layers {
		for _, block := range layer.Blocks {
			if size, ok := seen[block.Digest]; ok && size == block.Size {
				continue
			}
			seen[block.Digest] = block.Size
			digest, size := block.Digest, block.Size
			if err := s.Ensure(ctx, digest, size, func(ctx context.Context) (io.ReadCloser, error) {
				return client.FetchBlob(ctx, ref, digest)
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func exportBuildRegistryCaches(ctx context.Context, plan buildPlan, opts buildOptions, s store.Store) error {
	if len(opts.CacheTo) == 0 {
		return nil
	}
	payload, blockDescs, err := buildRegistryCachePayloadForPlan(ctx, plan, s)
	if err != nil {
		return err
	}
	if len(payload.Entries) == 0 {
		return nil
	}
	for _, ref := range opts.CacheTo {
		if err := exportBuildRegistryCache(ctx, ref, opts, s, payload, blockDescs); err != nil {
			return err
		}
	}
	return nil
}

func buildRegistryCachePayloadForPlan(ctx context.Context, plan buildPlan, s store.Store) (buildRegistryCachePayload, []ociimage.Descriptor, error) {
	payload := buildRegistryCachePayload{
		Version:   buildRegistryCacheVersion,
		CreatedAt: time.Now().UTC(),
	}
	layerSeen := map[string]bool{}
	blockDescs := map[string]ociimage.Descriptor{}
	for _, step := range plan.Steps {
		entry, err := loadBuildCacheEntry(s, step.Key)
		if err != nil {
			return payload, nil, fmt.Errorf("cove build: export cache key %s: %w", step.Key, err)
		}
		if err := validateBuildCacheEntryForStep(entry, step); err != nil {
			return payload, nil, fmt.Errorf("cove build: export cache key %s: %w", step.Key, err)
		}
		layer, err := loadBuildLayerManifest(s, entry.LayerDigest)
		if err != nil {
			return payload, nil, fmt.Errorf("cove build: export cache layer %s: %w", entry.LayerDigest, err)
		}
		if err := validateBuildLayerBlobs(ctx, s, layer); err != nil {
			return payload, nil, fmt.Errorf("cove build: export cache layer %s: %w", entry.LayerDigest, err)
		}
		payload.Entries = append(payload.Entries, entry)
		if !layerSeen[layer.Digest] {
			payload.Layers = append(payload.Layers, layer)
			layerSeen[layer.Digest] = true
		}
		for _, block := range layer.Blocks {
			if desc, ok := blockDescs[block.Digest]; ok {
				if desc.Size != block.Size {
					return payload, nil, fmt.Errorf("cove build: block %s size %d, want %d", block.Digest, desc.Size, block.Size)
				}
				continue
			}
			blockDescs[block.Digest] = ociimage.Descriptor{
				MediaType: buildRegistryCacheBlockMediaType,
				Size:      block.Size,
				Digest:    block.Digest,
			}
		}
	}
	descs := make([]ociimage.Descriptor, 0, len(blockDescs))
	for _, desc := range blockDescs {
		descs = append(descs, desc)
	}
	sort.Slice(descs, func(i, j int) bool {
		return descs[i].Digest < descs[j].Digest
	})
	return payload, descs, nil
}

func exportBuildRegistryCache(ctx context.Context, refText string, opts buildOptions, s store.Store, payload buildRegistryCachePayload, blockDescs []ociimage.Descriptor) error {
	ref, err := ociimage.ParseReference(refText)
	if err != nil {
		return fmt.Errorf("cove build: parse --cache-to: %w", err)
	}
	client := buildRegistryCacheClient(ref, opts)
	configJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("cove build: encode cache config: %w", err)
	}
	configDesc := ociimage.Descriptor{
		MediaType: buildRegistryCacheConfigMediaType,
		Size:      int64(len(configJSON)),
		Digest:    digestBytes(configJSON),
	}
	if err := uploadBytesBlob(ctx, client, ref, configDesc, configJSON); err != nil {
		return fmt.Errorf("cove build: upload cache config: %w", err)
	}
	for _, desc := range blockDescs {
		path, err := s.BlobPath(desc.Digest)
		if err != nil {
			return err
		}
		if err := uploadFileBlob(ctx, client, ref, desc, path); err != nil {
			return fmt.Errorf("cove build: upload cache block %s: %w", desc.Digest, err)
		}
	}
	manifest := ociimage.Manifest{
		SchemaVersion: 2,
		MediaType:     ociimage.MediaTypeImageManifest,
		Config:        configDesc,
		Layers:        blockDescs,
		Annotations: map[string]string{
			"org.tmc.cove.kind":                "build-cache",
			"org.tmc.cove.build-cache.version": fmt.Sprint(buildRegistryCacheVersion),
		},
	}
	if _, err := client.PushManifest(ctx, ref, manifest); err != nil {
		return fmt.Errorf("cove build: push cache manifest: %w", err)
	}
	return nil
}

func buildRegistryCacheClient(ref ociimage.Reference, opts buildOptions) ociimage.RegistryClient {
	return ociimage.RegistryClient{
		BaseURL:       opts.RegistryBaseURL,
		Authorization: registryAuthorization(ref, opts.RegistryToken),
		TokenCache:    ociimage.NewRegistryTokenCache(),
	}
}
