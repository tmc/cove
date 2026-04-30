package main

import (
	"context"
	"fmt"

	"github.com/tmc/vz-macos/internal/store"
)

type buildApplyResult struct {
	Step        string
	Key         string
	LayerDigest string
	Scratch     buildScratch
	DiskPath    string
}

func (e *buildExecutor) applyCacheHit(ctx context.Context, step buildPlanStep, parentDisk string) (buildApplyResult, error) {
	var result buildApplyResult
	if ctx == nil {
		ctx = context.Background()
	}
	entry, manifest, err := e.loadCacheHitLayer(ctx, step)
	if err != nil {
		return result, err
	}
	sc, err := e.createScratch("")
	if err != nil {
		return result, err
	}
	result = buildApplyResult{
		Step:        step.Name,
		Key:         step.Key,
		LayerDigest: entry.LayerDigest,
		Scratch:     sc,
		DiskPath:    sc.DiskPath,
	}
	if err := ApplyStoredDiskDelta(ctx, e.store, parentDisk, sc.DiskPath, manifest); err != nil {
		if e.opts.KeepIntermediate {
			return result, err
		}
		if cleanupErr := e.cleanupScratch(sc); cleanupErr != nil {
			return result, fmt.Errorf("%v; cleanup: %w", err, cleanupErr)
		}
		return result, err
	}
	return result, nil
}

func (e *buildExecutor) applyCacheHitVM(ctx context.Context, step buildPlanStep, parentDir string) (buildApplyResult, error) {
	var result buildApplyResult
	if ctx == nil {
		ctx = context.Background()
	}
	entry, manifest, err := e.loadCacheHitLayer(ctx, step)
	if err != nil {
		return result, err
	}
	parentDisk, err := pushDiskPath(parentDir)
	if err != nil {
		return result, err
	}
	sc, err := e.createScratchVM(parentDir)
	if err != nil {
		return result, err
	}
	result = buildApplyResult{
		Step:        step.Name,
		Key:         step.Key,
		LayerDigest: entry.LayerDigest,
		Scratch:     sc,
		DiskPath:    sc.DiskPath,
	}
	if err := ApplyStoredDiskDelta(ctx, e.store, parentDisk, sc.DiskPath, manifest); err != nil {
		if e.opts.KeepIntermediate {
			return result, err
		}
		if cleanupErr := e.cleanupScratch(sc); cleanupErr != nil {
			return result, fmt.Errorf("%v; cleanup: %w", err, cleanupErr)
		}
		return result, err
	}
	return result, nil
}

func (e *buildExecutor) loadCacheHitLayer(ctx context.Context, step buildPlanStep) (buildCacheEntry, buildLayerManifest, error) {
	var entry buildCacheEntry
	var manifest buildLayerManifest
	if ctx == nil {
		ctx = context.Background()
	}
	if _, _, err := splitStoreDigest(step.Key); err != nil {
		return entry, manifest, fmt.Errorf("apply cache hit: key: %w", err)
	}
	entry, err := loadBuildCacheEntry(e.store, step.Key)
	if err != nil {
		return entry, manifest, err
	}
	manifest, err = loadBuildLayerManifest(e.store, entry.LayerDigest)
	if err != nil {
		return entry, manifest, err
	}
	if err := validateBuildLayerBlobs(ctx, e.store, manifest); err != nil {
		return entry, manifest, err
	}
	return entry, manifest, nil
}

func validateBuildLayerBlobs(ctx context.Context, s store.Store, manifest buildLayerManifest) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for i, block := range manifest.Blocks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		f, err := s.OpenVerified(block.Digest, block.Size)
		if err != nil {
			return fmt.Errorf("build layer block %d: %w", i, err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("build layer block %d: close: %w", i, err)
		}
	}
	return nil
}
