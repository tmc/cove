package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tmc/cove/internal/ociimage"
	"github.com/tmc/cove/internal/store"
	"github.com/tmc/cove/internal/vmconfig"
)

type buildRegistryBaseMeta struct {
	Ref            string    `json:"ref"`
	ManifestDigest string    `json:"manifest_digest"`
	Format         string    `json:"format"`
	DiskFormat     string    `json:"disk_format,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

func materializeBuildRegistryBase(ctx context.Context, refText string, opts buildOptions) (string, func() error, error) {
	ref, err := ociimage.ParseReference(refText)
	if err != nil {
		return "", nil, fmt.Errorf("cove build: parse registry base: %w", err)
	}
	if ref.Digest != "" {
		dir, ok, err := cachedBuildRegistryBase(opts, ref.Digest)
		if err != nil {
			return "", nil, err
		}
		if ok {
			return dir, noopBuildRegistryBaseCleanup, nil
		}
	}
	pullOpts := pullOptions{
		RegistryBaseURL: opts.RegistryBaseURL,
		RegistryToken:   opts.RegistryToken,
		StoreDir:        opts.StoreDir,
	}
	parsed, resolution, manifestRaw, err := fetchPullManifest(ctx, ref, pullOpts)
	if err != nil {
		return "", nil, fmt.Errorf("cove build: fetch registry base: %w", err)
	}
	manifestDigest := resolution.Digest
	if dir, ok, err := cachedBuildRegistryBase(opts, manifestDigest); err != nil {
		return "", nil, err
	} else if ok {
		return dir, noopBuildRegistryBaseCleanup, nil
	}
	dir, err := newBuildRegistryBaseTempDir(opts, manifestDigest)
	if err != nil {
		return "", nil, err
	}
	cleanupTemp := func() {
		_ = os.RemoveAll(dir)
	}
	plan := &pullPlan{
		Ref:                ref,
		VMName:             filepath.Base(dir) + ".covevm",
		VMDir:              dir,
		Manifest:           parsed,
		ManifestRaw:        manifestRaw,
		ManifestDigest:     manifestDigest,
		ManifestResolution: resolution,
	}
	if err := pullBuildRegistryBase(ctx, plan, pullOpts); err != nil {
		cleanupTemp()
		return "", nil, err
	}
	if err := writeBuildRegistryBaseMeta(dir, refText, manifestDigest, parsed.Format, parsed.Annotations.DiskFormat); err != nil {
		cleanupTemp()
		return "", nil, err
	}
	finalDir, err := promoteBuildRegistryBaseCache(opts, manifestDigest, dir)
	if err != nil {
		cleanupTemp()
		return "", nil, err
	}
	return finalDir, noopBuildRegistryBaseCleanup, nil
}

func cachedBuildRegistryBase(opts buildOptions, digest string) (string, bool, error) {
	dir, err := buildRegistryBaseCacheDir(opts, digest)
	if err != nil {
		return "", false, err
	}
	if validBuildRegistryBaseCache(dir, digest) {
		return dir, true, nil
	}
	if _, err := os.Stat(dir); err == nil {
		if err := os.RemoveAll(dir); err != nil {
			return "", false, fmt.Errorf("remove stale registry base cache %s: %w", dir, err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return "", false, fmt.Errorf("stat registry base cache %s: %w", dir, err)
	}
	return dir, false, nil
}

func buildRegistryBaseCacheDir(opts buildOptions, digest string) (string, error) {
	_, hexDigest, err := splitStoreDigest(digest)
	if err != nil {
		return "", fmt.Errorf("cove build: registry base digest: %w", err)
	}
	return filepath.Join(buildRegistryBaseCacheRoot(store.New(opts.StoreDir).Dir), hexDigest), nil
}

func buildRegistryBaseCacheRoot(storeDir string) string {
	return filepath.Join(storeDir, "build-registry-bases", "sha256")
}

func newBuildRegistryBaseTempDir(opts buildOptions, digest string) (string, error) {
	cacheDir, err := buildRegistryBaseCacheDir(opts, digest)
	if err != nil {
		return "", err
	}
	root := filepath.Join(filepath.Dir(cacheDir), ".tmp")
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", fmt.Errorf("cove build: create registry base cache: %w", err)
	}
	dir, err := os.MkdirTemp(root, digestFileName(digest)+"-")
	if err != nil {
		return "", fmt.Errorf("cove build: create registry base cache temp: %w", err)
	}
	return dir, nil
}

func promoteBuildRegistryBaseCache(opts buildOptions, digest, dir string) (string, error) {
	finalDir, err := buildRegistryBaseCacheDir(opts, digest)
	if err != nil {
		return "", err
	}
	if err := os.Rename(dir, finalDir); err != nil {
		if validBuildRegistryBaseCache(finalDir, digest) {
			_ = os.RemoveAll(dir)
			return finalDir, nil
		}
		return "", fmt.Errorf("promote registry base cache: %w", err)
	}
	return finalDir, nil
}

func validBuildRegistryBaseCache(dir, digest string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "disk.provenance"))
	if err != nil || strings.TrimSpace(string(data)) != digest {
		return false
	}
	if !vmconfig.Validate(dir) {
		return false
	}
	if _, err := pushDiskPath(dir); err != nil {
		return false
	}
	return true
}

func writeBuildRegistryBaseMeta(dir, refText, manifestDigest string, format ociimage.ManifestFormat, diskFormat string) error {
	meta := buildRegistryBaseMeta{
		Ref:            refText,
		ManifestDigest: manifestDigest,
		Format:         format.String(),
		DiskFormat:     diskFormat,
		CreatedAt:      time.Now().UTC(),
	}
	return writeBuildCacheJSON(filepath.Join(dir, "build-registry-base.json"), meta)
}

func noopBuildRegistryBaseCleanup() error {
	return nil
}

func pullBuildRegistryBase(ctx context.Context, plan *pullPlan, opts pullOptions) error {
	switch plan.Manifest.Format {
	case ociimage.FormatLume:
		if err := lumePullDisk(ctx, plan, opts); err != nil {
			return fmt.Errorf("cove build: pull lume registry base: %w", err)
		}
	case ociimage.FormatTart:
		if err := tartPullDisk(ctx, plan, opts); err != nil {
			return fmt.Errorf("cove build: pull tart registry base: %w", err)
		}
	default:
		if err := pullDisk(ctx, plan, opts); err != nil {
			return fmt.Errorf("cove build: pull registry base: %w", err)
		}
	}
	return nil
}
