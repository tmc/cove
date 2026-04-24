package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tmc/vz-macos/internal/ociimage"
)

const pullManifestFetchTimeout = 30 * time.Second

type pullOptions struct {
	As              string
	DryRun          bool
	ManifestPath    string
	RegistryBaseURL string
	RegistryToken   string
}

type pullPlan struct {
	Ref            ociimage.Reference
	VMName         string
	VMDir          string
	Manifest       ociimage.ParsedManifest
	ManifestDigest string
}

func handlePull(args []string) error {
	opts, pos, err := parsePullArgs(args, os.Stderr)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("usage: cove pull <ref> [flags]")
	}
	if !opts.DryRun {
		return fmt.Errorf("cove pull: disk download is not implemented yet; use --dry-run to validate a manifest")
	}
	plan, err := buildPullPlan(pos[0], opts)
	if err != nil {
		return err
	}
	printPullDryRun(os.Stdout, plan)
	return nil
}

func parsePullArgs(args []string, w io.Writer) (pullOptions, []string, error) {
	var opts pullOptions
	fs := flag.NewFlagSet("pull", flag.ContinueOnError)
	fs.SetOutput(w)
	fs.StringVar(&opts.As, "as", "", "destination VM name")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "validate inputs without writing a disk")
	fs.StringVar(&opts.ManifestPath, "manifest", "", "local OCI manifest JSON instead of fetching the registry")
	fs.Usage = func() { printPullUsage(w) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return opts, nil, nil
		}
		return opts, nil, err
	}
	return opts, fs.Args(), nil
}

func buildPullPlan(refText string, opts pullOptions) (*pullPlan, error) {
	ref, err := ociimage.ParseReference(refText)
	if err != nil {
		return nil, fmt.Errorf("cove pull: invalid ref %q: %w", refText, err)
	}
	if ref.Tag == "" && ref.Digest == "" {
		return nil, fmt.Errorf("cove pull: ref %q must include a tag or digest", refText)
	}
	name := strings.TrimSpace(opts.As)
	if name == "" {
		name = pullNameFromReference(ref)
	}
	if name == "" {
		return nil, fmt.Errorf("cove pull: destination VM name is empty")
	}
	vmDirectory := GetVMPath(name)
	if err := checkPullTarget(vmDirectory); err != nil {
		return nil, err
	}

	var (
		parsed         ociimage.ParsedManifest
		manifestDigest string
	)
	if opts.ManifestPath != "" {
		parsed, err = readPullManifest(opts.ManifestPath)
		if err != nil {
			return nil, err
		}
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), pullManifestFetchTimeout)
		defer cancel()
		parsed, manifestDigest, err = fetchPullManifest(ctx, ref, opts)
		if err != nil {
			return nil, err
		}
	}
	return &pullPlan{
		Ref:            ref,
		VMName:         name,
		VMDir:          vmDirectory,
		Manifest:       parsed,
		ManifestDigest: manifestDigest,
	}, nil
}

func checkPullTarget(vmDirectory string) error {
	diskPath := filepath.Join(vmDirectory, "disk.img")
	if _, err := os.Stat(diskPath); err == nil {
		if err := ensurePullTargetInactive(vmDirectory); err != nil {
			return err
		}
		return checkIncompletePullDisk(vmDirectory, diskPath)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("stat disk: %w", err)
	}
	return checkIncompletePullDisk(vmDirectory, diskPath)
}

func readPullManifest(path string) (ociimage.ParsedManifest, error) {
	var out ociimage.ParsedManifest
	data, err := os.ReadFile(path)
	if err != nil {
		return out, fmt.Errorf("read manifest: %w", err)
	}
	var manifest ociimage.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return out, fmt.Errorf("parse manifest JSON: %w", err)
	}
	out, err = ociimage.ParseManifest(manifest)
	if err != nil {
		return out, err
	}
	return out, nil
}

func fetchPullManifest(ctx context.Context, ref ociimage.Reference, opts pullOptions) (ociimage.ParsedManifest, string, error) {
	var out ociimage.ParsedManifest
	client := ociimage.RegistryClient{
		BaseURL: opts.RegistryBaseURL,
		Token:   pullRegistryToken(ref, opts),
	}
	manifest, digest, err := client.FetchManifest(ctx, ref)
	if err != nil {
		return out, "", err
	}
	out, err = ociimage.ParseManifest(manifest)
	if err != nil {
		return out, "", fmt.Errorf("parse registry manifest: %w", err)
	}
	return out, digest, nil
}

func pullRegistryToken(ref ociimage.Reference, opts pullOptions) string {
	if opts.RegistryToken != "" {
		return opts.RegistryToken
	}
	if token := strings.TrimSpace(os.Getenv("COVE_REGISTRY_TOKEN")); token != "" {
		return token
	}
	if ref.Registry == "ghcr.io" {
		return strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	}
	return ""
}

func pullNameFromReference(ref ociimage.Reference) string {
	parts := strings.Split(ref.Repository, "/")
	return parts[len(parts)-1]
}

func printPullDryRun(w io.Writer, plan *pullPlan) {
	fmt.Fprintln(w, "Pull dry run")
	fmt.Fprintf(w, "  ref: %s\n", plan.Ref.String())
	fmt.Fprintf(w, "  vm: %s\n", plan.VMName)
	fmt.Fprintf(w, "  target: %s\n", plan.VMDir)
	if plan.ManifestDigest != "" {
		fmt.Fprintf(w, "  manifest digest: %s\n", plan.ManifestDigest)
	}
	if len(plan.Manifest.Chunks) == 0 && plan.Manifest.Annotations.UncompressedDiskSize == 0 {
		fmt.Fprintln(w, "  manifest: not provided")
		return
	}
	fmt.Fprintf(w, "  disk size: %s\n", FormatSize(plan.Manifest.Annotations.UncompressedDiskSize))
	fmt.Fprintf(w, "  chunks: %d\n", len(plan.Manifest.Chunks))
	fmt.Fprintf(w, "  metadata blobs: %d\n", len(plan.Manifest.Blobs))
}

func printPullUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove pull <ref> [flags]

Validate or pull an OCI VM image.

Current implementation supports --dry-run manifest validation. Without
--manifest, dry-run fetches the registry manifest. Chunk download,
decompression, and disk writes are wired in later OCI slices.

Flags:
  --as <name>          Destination VM name
  --dry-run            Validate inputs without writing a disk
  --manifest <path>    Local OCI manifest JSON instead of fetching the registry`)
}
