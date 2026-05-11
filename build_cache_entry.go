package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tmc/vz-macos/internal/store"
)

type buildCacheEntry struct {
	Key                  string    `json:"key"`
	ParentDigest         string    `json:"parent_digest"`
	ScriptDigest         string    `json:"script_digest"`
	AgentProtocolVersion string    `json:"agent_protocol_version"`
	Compact              string    `json:"compact"`
	LayerDigest          string    `json:"layer_digest"`
	CreatedAt            time.Time `json:"created_at"`
}

func saveBuildLayerManifest(s store.Store, manifest buildLayerManifest) error {
	if err := validateBuildLayerManifest(manifest); err != nil {
		return fmt.Errorf("save build layer: %w", err)
	}
	return writeBuildCacheJSON(filepath.Join(s.Dir, "build-cache", "layers", digestFileName(manifest.Digest)+".json"), manifest)
}

func loadBuildLayerManifest(s store.Store, digest string) (buildLayerManifest, error) {
	var manifest buildLayerManifest
	if _, _, err := splitStoreDigest(digest); err != nil {
		return manifest, err
	}
	data, err := os.ReadFile(filepath.Join(s.Dir, "build-cache", "layers", digestFileName(digest)+".json"))
	if err != nil {
		return manifest, fmt.Errorf("load build layer: %w", err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, fmt.Errorf("load build layer: %w", err)
	}
	if manifest.Digest != digest {
		return manifest, fmt.Errorf("load build layer: digest %s, want %s", manifest.Digest, digest)
	}
	if err := validateBuildLayerManifest(manifest); err != nil {
		return manifest, fmt.Errorf("load build layer: %w", err)
	}
	return manifest, nil
}

func saveBuildCacheEntry(s store.Store, entry buildCacheEntry) error {
	if err := validateBuildCacheEntry(entry); err != nil {
		return fmt.Errorf("save build cache entry: %w", err)
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	return writeBuildCacheJSON(filepath.Join(s.Dir, "build-cache", "keys", digestFileName(entry.Key)+".json"), entry)
}

func loadBuildCacheEntry(s store.Store, key string) (buildCacheEntry, error) {
	var entry buildCacheEntry
	if _, _, err := splitStoreDigest(key); err != nil {
		return entry, err
	}
	data, err := os.ReadFile(filepath.Join(s.Dir, "build-cache", "keys", digestFileName(key)+".json"))
	if err != nil {
		return entry, fmt.Errorf("load build cache entry: %w", err)
	}
	if err := json.Unmarshal(data, &entry); err != nil {
		return entry, fmt.Errorf("load build cache entry: %w", err)
	}
	if entry.Key != key {
		return entry, fmt.Errorf("load build cache entry: key %s, want %s", entry.Key, key)
	}
	if err := validateBuildCacheEntry(entry); err != nil {
		return entry, fmt.Errorf("load build cache entry: %w", err)
	}
	return entry, nil
}

func validateBuildCacheEntry(entry buildCacheEntry) error {
	if entry.Key == "" {
		return fmt.Errorf("empty key")
	}
	if _, _, err := splitStoreDigest(entry.Key); err != nil {
		return err
	}
	if entry.LayerDigest == "" {
		return fmt.Errorf("empty layer digest")
	}
	if _, _, err := splitStoreDigest(entry.LayerDigest); err != nil {
		return fmt.Errorf("layer digest: %w", err)
	}
	for name, digest := range map[string]string{
		"parent digest": entry.ParentDigest,
		"script digest": entry.ScriptDigest,
	} {
		if digest == "" {
			return fmt.Errorf("empty %s", name)
		}
		if _, _, err := splitStoreDigest(digest); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	if entry.AgentProtocolVersion == "" {
		return fmt.Errorf("empty agent protocol version")
	}
	if err := validateCompactMode(entry.Compact); err != nil {
		return fmt.Errorf("compact: %w", err)
	}
	return nil
}

func validateBuildLayerManifest(manifest buildLayerManifest) error {
	if _, _, err := splitStoreDigest(manifest.Digest); err != nil {
		return fmt.Errorf("digest: %w", err)
	}
	if manifest.BlockSize <= 0 {
		return fmt.Errorf("invalid block size %d", manifest.BlockSize)
	}
	if manifest.DiskSize < 0 {
		return fmt.Errorf("invalid disk size %d", manifest.DiskSize)
	}
	for i, block := range manifest.Blocks {
		if block.Offset < 0 {
			return fmt.Errorf("block %d: invalid offset %d", i, block.Offset)
		}
		if block.Size <= 0 {
			return fmt.Errorf("block %d: invalid size %d", i, block.Size)
		}
		if block.Size > manifest.BlockSize {
			return fmt.Errorf("block %d: size %d exceeds block size %d", i, block.Size, manifest.BlockSize)
		}
		if block.Offset%manifest.BlockSize != 0 {
			return fmt.Errorf("block %d: unaligned offset %d", i, block.Offset)
		}
		if block.Offset+block.Size > manifest.DiskSize {
			return fmt.Errorf("block %d: range exceeds disk size", i)
		}
		if _, _, err := splitStoreDigest(block.Digest); err != nil {
			return fmt.Errorf("block %d digest: %w", i, err)
		}
	}
	want, err := digestBuildLayerManifest(manifest)
	if err != nil {
		return err
	}
	if manifest.Digest != want {
		return fmt.Errorf("digest %s, want %s", manifest.Digest, want)
	}
	return nil
}

func writeBuildCacheJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create build cache dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return fmt.Errorf("create build cache temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write build cache: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync build cache: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close build cache: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename build cache: %w", err)
	}
	return nil
}

func digestFileName(digest string) string {
	_, hexDigest, _ := splitStoreDigest(digest)
	return hexDigest
}

func splitStoreDigest(digest string) (string, string, error) {
	algo, hexDigest, ok := strings.Cut(digest, ":")
	if !ok || algo == "" || hexDigest == "" {
		return "", "", fmt.Errorf("invalid digest %q", digest)
	}
	if algo != "sha256" || len(hexDigest) != 64 {
		return "", "", fmt.Errorf("invalid digest %q", digest)
	}
	for _, r := range hexDigest {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return "", "", fmt.Errorf("invalid digest %q", digest)
		}
	}
	return algo, hexDigest, nil
}
