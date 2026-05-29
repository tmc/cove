package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/tmc/cove/internal/store"
)

type buildLayerManifest struct {
	Digest    string            `json:"digest"`
	BlockSize int64             `json:"block_size"`
	DiskSize  int64             `json:"disk_size"`
	Blocks    []buildLayerBlock `json:"blocks"`
}

type buildLayerBlock struct {
	Offset int64  `json:"offset"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

func StoreDiskDelta(s store.Store, delta *diskDelta) (buildLayerManifest, error) {
	var manifest buildLayerManifest
	if delta == nil {
		return manifest, fmt.Errorf("store delta: nil delta")
	}
	manifest.BlockSize = delta.BlockSize
	manifest.DiskSize = delta.Size
	for _, block := range delta.Blocks {
		digest := digestBytes(block.Data)
		if err := s.Put(digest, int64(len(block.Data)), bytes.NewReader(block.Data)); err != nil {
			return manifest, fmt.Errorf("store delta block at %d: %w", block.Offset, err)
		}
		manifest.Blocks = append(manifest.Blocks, buildLayerBlock{Offset: block.Offset, Size: int64(len(block.Data)), Digest: digest})
	}
	digest, err := digestBuildLayerManifest(manifest)
	if err != nil {
		return manifest, fmt.Errorf("store delta manifest: %w", err)
	}
	manifest.Digest = digest
	return manifest, nil
}

func digestBuildLayerManifest(manifest buildLayerManifest) (string, error) {
	manifest.Digest = ""
	data, err := json.Marshal(manifest)
	if err != nil {
		return "", fmt.Errorf("digest build layer: %w", err)
	}
	return digestBytes(data), nil
}

func ApplyStoredDiskDelta(ctx context.Context, s store.Store, parentPath, childPath string, manifest buildLayerManifest) error {
	if err := validateBuildLayerManifest(manifest); err != nil {
		return fmt.Errorf("apply stored delta: %w", err)
	}
	delta := &diskDelta{BlockSize: manifest.BlockSize, Size: manifest.DiskSize}
	for _, block := range manifest.Blocks {
		f, err := s.OpenVerified(block.Digest, block.Size)
		if err != nil {
			return fmt.Errorf("open delta block at %d: %w", block.Offset, err)
		}
		data, err := readAllContext(ctx, f)
		closeErr := f.Close()
		if err != nil {
			return fmt.Errorf("read delta block at %d: %w", block.Offset, err)
		}
		if closeErr != nil {
			return fmt.Errorf("close delta block at %d: %w", block.Offset, closeErr)
		}
		delta.Blocks = append(delta.Blocks, diskDeltaBlock{Offset: block.Offset, Data: data})
	}
	return ApplyDiskDelta(parentPath, childPath, delta)
}

func readAllContext(ctx context.Context, r io.Reader) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	type result struct {
		data []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		data, err := io.ReadAll(r)
		ch <- result{data: data, err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		return res.data, res.err
	}
}
