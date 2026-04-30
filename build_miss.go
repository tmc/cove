package main

import (
	"context"
	"fmt"
)

func (e *buildExecutor) recordCacheMissLayer(ctx context.Context, step buildPlanStep, parentDisk, childDisk string) (buildApplyResult, error) {
	var result buildApplyResult
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return result, ctx.Err()
	default:
	}
	if _, _, err := splitStoreDigest(step.Key); err != nil {
		return result, fmt.Errorf("record cache miss: key: %w", err)
	}
	if parentDisk == "" {
		return result, fmt.Errorf("record cache miss: parent disk path required")
	}
	if childDisk == "" {
		return result, fmt.Errorf("record cache miss: child disk path required")
	}
	delta, err := DiffDisks(parentDisk, childDisk)
	if err != nil {
		return result, fmt.Errorf("record cache miss: %w", err)
	}
	manifest, err := StoreDiskDelta(e.store, delta)
	if err != nil {
		return result, fmt.Errorf("record cache miss: %w", err)
	}
	if err := saveBuildLayerManifest(e.store, manifest); err != nil {
		return result, fmt.Errorf("record cache miss: %w", err)
	}
	entry := buildCacheEntry{
		Key:                  step.Key,
		ParentDigest:         step.ParentDigest,
		ScriptDigest:         step.ScriptDigest,
		AgentProtocolVersion: step.AgentProtocolVersion,
		Compact:              step.Meta.Compact,
		LayerDigest:          manifest.Digest,
	}
	if entry.AgentProtocolVersion == "" {
		entry.AgentProtocolVersion = agentProtocolVersion
	}
	if err := saveBuildCacheEntry(e.store, entry); err != nil {
		return result, fmt.Errorf("record cache miss: %w", err)
	}
	return buildApplyResult{
		Step:        step.Name,
		Key:         step.Key,
		LayerDigest: manifest.Digest,
		DiskPath:    childDisk,
	}, nil
}
