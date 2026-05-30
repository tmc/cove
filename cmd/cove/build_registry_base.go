package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tmc/cove/internal/ociimage"
)

func materializeBuildRegistryBase(ctx context.Context, refText string, opts buildOptions) (string, func() error, error) {
	ref, err := ociimage.ParseReference(refText)
	if err != nil {
		return "", nil, fmt.Errorf("cove build: parse registry base: %w", err)
	}
	root := defaultBuildScratchRoot()
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", nil, fmt.Errorf("cove build: create build scratch root: %w", err)
	}
	dir, err := os.MkdirTemp(root, "registry-base-")
	if err != nil {
		return "", nil, fmt.Errorf("cove build: create registry base scratch: %w", err)
	}
	cleanup := func() error {
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("remove registry base scratch %s: %w", dir, err)
		}
		return nil
	}
	pullOpts := pullOptions{
		RegistryBaseURL: opts.RegistryBaseURL,
		RegistryToken:   opts.RegistryToken,
	}
	parsed, manifestDigest, manifestRaw, err := fetchPullManifest(ctx, ref, pullOpts)
	if err != nil {
		_ = cleanup()
		return "", nil, fmt.Errorf("cove build: fetch registry base: %w", err)
	}
	plan := &pullPlan{
		Ref:            ref,
		VMName:         filepath.Base(dir) + ".covevm",
		VMDir:          dir,
		Manifest:       parsed,
		ManifestRaw:    manifestRaw,
		ManifestDigest: manifestDigest,
	}
	if err := pullBuildRegistryBase(ctx, plan, pullOpts); err != nil {
		_ = cleanup()
		return "", nil, err
	}
	return dir, cleanup, nil
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
