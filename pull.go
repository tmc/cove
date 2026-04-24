package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tmc/vz-macos/internal/bytefmt"
	"github.com/tmc/vz-macos/internal/ociimage"
)

const (
	pullManifestFetchTimeout = 30 * time.Second
	pullChunkWorkers         = 4
)

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
	plan, err := buildPullPlan(pos[0], opts)
	if err != nil {
		return err
	}
	if opts.DryRun {
		printPullDryRun(os.Stdout, plan)
		return nil
	}
	if err := pullDisk(context.Background(), plan, opts); err != nil {
		return err
	}
	printPullResult(os.Stdout, plan)
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
	client := pullRegistryClient(ref, opts)
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

func pullDisk(ctx context.Context, plan *pullPlan, opts pullOptions) error {
	if plan == nil {
		return fmt.Errorf("cove pull: missing pull plan")
	}
	if len(plan.Manifest.DiskLayers) == 0 {
		return fmt.Errorf("cove pull: manifest has no disk chunks")
	}
	if err := checkPullTarget(plan.VMDir); err != nil {
		return err
	}
	if err := os.MkdirAll(plan.VMDir, 0755); err != nil {
		return fmt.Errorf("create VM directory: %w", err)
	}

	partialPath := filepath.Join(plan.VMDir, "disk.img.partial")
	diskPath := filepath.Join(plan.VMDir, "disk.img")
	disk, err := ociimage.CreatePartialDisk(partialPath, plan.Manifest.Annotations.UncompressedDiskSize)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			disk.Close()
		}
	}()

	client := pullRegistryClient(plan.Ref, opts)
	if err := pullDiskChunks(ctx, client, plan, disk); err != nil {
		return err
	}
	if err := pullMetadataBlobs(ctx, client, plan); err != nil {
		return err
	}
	if err := disk.Sync(); err != nil {
		return fmt.Errorf("sync partial disk: %w", err)
	}
	if err := disk.Close(); err != nil {
		return fmt.Errorf("close partial disk: %w", err)
	}
	closed = true
	if err := os.Rename(partialPath, diskPath); err != nil {
		return fmt.Errorf("rename partial disk: %w", err)
	}
	if err := writePullProvenance(plan.VMDir, plan.ManifestDigest); err != nil {
		return err
	}
	if err := syncPullDir(plan.VMDir); err != nil {
		return fmt.Errorf("fsync VM directory: %w", err)
	}
	return nil
}

func pullDiskChunks(ctx context.Context, client ociimage.RegistryClient, plan *pullPlan, disk io.WriterAt) error {
	layers := nonZeroDiskLayers(plan.Manifest.DiskLayers)
	if len(layers) == 0 {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan ociimage.DiskLayer)
	errc := make(chan error, 1)
	var wg sync.WaitGroup
	workers := pullChunkWorkers
	if len(layers) < workers {
		workers = len(layers)
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for layer := range jobs {
				if err := pullDiskChunk(ctx, client, plan.Ref, disk, layer); err != nil {
					select {
					case errc <- err:
					default:
					}
					cancel()
					return
				}
			}
		}()
	}
send:
	for _, layer := range layers {
		select {
		case jobs <- layer:
		case <-ctx.Done():
			break send
		}
	}
	close(jobs)
	wg.Wait()
	select {
	case err := <-errc:
		return err
	default:
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func nonZeroDiskLayers(layers []ociimage.DiskLayer) []ociimage.DiskLayer {
	out := make([]ociimage.DiskLayer, 0, len(layers))
	for _, layer := range layers {
		if !layer.Chunk.Zero {
			out = append(out, layer)
		}
	}
	return out
}

func pullDiskChunk(ctx context.Context, client ociimage.RegistryClient, ref ociimage.Reference, disk io.WriterAt, layer ociimage.DiskLayer) error {
	body, err := client.FetchBlob(ctx, ref, layer.Descriptor.Digest)
	if err != nil {
		return err
	}
	err = ociimage.WriteCompressedChunkAt(disk, layer.Chunk, layer.Descriptor, body)
	closeErr := body.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return fmt.Errorf("close blob: %w", closeErr)
	}
	return nil
}

func pullMetadataBlobs(ctx context.Context, client ociimage.RegistryClient, plan *pullPlan) error {
	for _, desc := range plan.Manifest.Blobs {
		name, ok, err := pullMetadataFileName(desc)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		path := filepath.Join(plan.VMDir, name)
		if err := pullBlobToFile(ctx, client, plan.Ref, desc, path); err != nil {
			return err
		}
	}
	return nil
}

func pullMetadataFileName(desc ociimage.Descriptor) (string, bool, error) {
	ann, err := ociimage.NormalizeLayerAnnotations(desc.Annotations)
	if err != nil {
		return "", false, fmt.Errorf("parse metadata layer: %w", err)
	}
	switch ann.Role {
	case "nvram":
		return "aux.img", true, nil
	case "hw-model":
		return "hw.model", true, nil
	case "machine-id":
		return "machine.id", true, nil
	default:
		return "", false, nil
	}
}

func pullBlobToFile(ctx context.Context, client ociimage.RegistryClient, ref ociimage.Reference, desc ociimage.Descriptor, path string) error {
	if desc.Digest == "" {
		return fmt.Errorf("pull metadata blob: missing digest")
	}
	if desc.Size < 0 {
		return fmt.Errorf("pull metadata blob: negative size %d", desc.Size)
	}
	body, err := client.FetchBlob(ctx, ref, desc.Digest)
	if err != nil {
		return err
	}
	defer body.Close()

	tmpPath := path + ".tmp"
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("open metadata blob: %w", err)
	}
	h := sha256.New()
	n, copyErr := io.Copy(f, io.TeeReader(body, h))
	if copyErr != nil {
		f.Close()
		return fmt.Errorf("write metadata blob: %w", copyErr)
	}
	if n != desc.Size {
		f.Close()
		return fmt.Errorf("write metadata blob: size %d, want %d", n, desc.Size)
	}
	if got := "sha256:" + hex.EncodeToString(h.Sum(nil)); got != desc.Digest {
		f.Close()
		return fmt.Errorf("write metadata blob: digest %s, want %s", got, desc.Digest)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync metadata blob: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close metadata blob: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename metadata blob: %w", err)
	}
	return nil
}

func pullRegistryClient(ref ociimage.Reference, opts pullOptions) ociimage.RegistryClient {
	return ociimage.RegistryClient{
		BaseURL:       opts.RegistryBaseURL,
		Authorization: registryAuthorization(ref, opts.RegistryToken),
		TokenCache:    ociimage.NewRegistryTokenCache(),
	}
}

func writePullProvenance(vmDir, digest string) error {
	if digest == "" {
		return nil
	}
	tmpPath := filepath.Join(vmDir, "disk.provenance.tmp")
	finalPath := filepath.Join(vmDir, "disk.provenance")
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("open provenance: %w", err)
	}
	if _, err := f.WriteString(digest + "\n"); err != nil {
		f.Close()
		return fmt.Errorf("write provenance: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync provenance: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close provenance: %w", err)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return fmt.Errorf("rename provenance: %w", err)
	}
	return nil
}

func syncPullDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
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
	fmt.Fprintf(w, "  disk size: %s\n", bytefmt.Size(plan.Manifest.Annotations.UncompressedDiskSize))
	fmt.Fprintf(w, "  chunks: %d\n", len(plan.Manifest.Chunks))
	fmt.Fprintf(w, "  metadata blobs: %d\n", len(plan.Manifest.Blobs))
}

func printPullResult(w io.Writer, plan *pullPlan) {
	fmt.Fprintln(w, "Pull complete")
	fmt.Fprintf(w, "  ref: %s\n", plan.Ref.String())
	fmt.Fprintf(w, "  vm: %s\n", plan.VMName)
	fmt.Fprintf(w, "  target: %s\n", plan.VMDir)
}

func printPullUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove pull <ref> [flags]

Validate or pull an OCI VM image.

Pull fetches the registry manifest, streams verified LZ4 disk chunks into
disk.img.partial, restores macOS identity metadata, and atomically renames the
verified disk into place. Use --dry-run to validate the manifest and target
without writing a disk.

Flags:
  --as <name>          Destination VM name
  --dry-run            Validate inputs without writing a disk
  --manifest <path>    Local OCI manifest JSON instead of fetching the registry`)
}
