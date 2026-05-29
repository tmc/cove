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
	if err := validateBuildCacheStepMetadata(step); err != nil {
		return result, fmt.Errorf("record cache miss: %w", err)
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

func validateBuildCacheStepMetadata(step buildPlanStep) error {
	for name, digest := range map[string]string{
		"parent digest": step.ParentDigest,
		"script digest": step.ScriptDigest,
	} {
		if digest == "" {
			return fmt.Errorf("empty %s", name)
		}
		if _, _, err := splitStoreDigest(digest); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	if step.AgentProtocolVersion == "" {
		return fmt.Errorf("empty agent protocol version")
	}
	if err := validateCompactMode(step.Meta.Compact); err != nil {
		return fmt.Errorf("compact: %w", err)
	}
	return nil
}
