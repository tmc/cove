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

	"github.com/tmc/cove/internal/bytefmt"
	"github.com/tmc/cove/internal/ociimage"
	"github.com/tmc/cove/internal/store"
	"github.com/tmc/cove/internal/vmconfig"
)

const (
	pullManifestFetchTimeout = 30 * time.Second
	pullChunkWorkers         = 4
)

type pullOptions struct {
	As              string
	DryRun          bool
	JSON            bool
	FetchManifest   bool
	VerifyBlobs     bool
	Resume          bool
	ManifestPath    string
	RegistryBaseURL string
	RegistryToken   string
	StoreDir        string
}

type pullPlan struct {
	Ref                  ociimage.Reference
	VMName               string
	VMDir                string
	Manifest             ociimage.ParsedManifest
	ManifestRaw          []byte
	ManifestDigest       string
	BaseReusePath        string
	BaseReuseDiskFormat  string
	BaseReuseChunks      int
	BaseReuseBytes       int64
	FetchDiskChunks      int
	FetchDiskBytes       int64
	StoreDiskChunks      int
	StoreDiskBytes       int64
	ZeroDiskChunks       int
	ZeroDiskBytes        int64
	FetchMetadataBlobs   int
	FetchMetadataBytes   int64
	StoreMetadataBlobs   int
	StoreMetadataBytes   int64
	BlobAudit            string
	BlobDescriptors      int
	BlobBytes            int64
	MissingBlobs         []string
	FetchBlobDescriptors []pullBlobDescriptor
}

type pullBlobDescriptor struct {
	Name       string
	Descriptor ociimage.Descriptor
}

type pullDryRunOutput struct {
	Ref              string                    `json:"ref"`
	VM               string                    `json:"vm"`
	Target           string                    `json:"target"`
	ManifestProvided bool                      `json:"manifest_provided"`
	ManifestDigest   string                    `json:"manifest_digest,omitempty"`
	Format           string                    `json:"format,omitempty"`
	DiskSize         int64                     `json:"disk_size,omitempty"`
	DiskFormat       string                    `json:"disk_format,omitempty"`
	Chunks           int                       `json:"chunks,omitempty"`
	MetadataBlobs    int                       `json:"metadata_blobs,omitempty"`
	DiskParts        int                       `json:"disk_parts,omitempty"`
	DiskLayers       int                       `json:"disk_layers,omitempty"`
	CompressedBytes  int64                     `json:"compressed_bytes,omitempty"`
	NVRAMBytes       int64                     `json:"nvram_bytes,omitempty"`
	ConfigBytes      int64                     `json:"config_bytes,omitempty"`
	UploadTime       string                    `json:"upload_time,omitempty"`
	Transfer         *pullDryRunTransferOutput `json:"transfer,omitempty"`
	BaseReuse        *pullBaseReuseOutput      `json:"base_reuse,omitempty"`
	BlobAudit        *pullBlobAuditOutput      `json:"blob_audit,omitempty"`
}

type pullDryRunTransferOutput struct {
	DiskFetchChunks    int   `json:"disk_fetch_chunks"`
	DiskFetchBytes     int64 `json:"disk_fetch_bytes"`
	DiskStoreChunks    int   `json:"disk_store_chunks"`
	DiskStoreBytes     int64 `json:"disk_store_bytes"`
	ZeroChunks         int   `json:"zero_chunks"`
	ZeroBytes          int64 `json:"zero_bytes"`
	MetadataFetchBlobs int   `json:"metadata_fetch_blobs"`
	MetadataFetchBytes int64 `json:"metadata_fetch_bytes"`
	MetadataStoreBlobs int   `json:"metadata_store_blobs"`
	MetadataStoreBytes int64 `json:"metadata_store_bytes"`
}

type pullBaseReuseOutput struct {
	Path       string `json:"path,omitempty"`
	DiskFormat string `json:"disk_format,omitempty"`
	Chunks     int    `json:"chunks"`
	Bytes      int64  `json:"bytes"`
}

type pullBlobAuditOutput struct {
	Status      string   `json:"status"`
	Descriptors int      `json:"descriptors"`
	Bytes       int64    `json:"bytes"`
	Missing     []string `json:"missing,omitempty"`
}

func handlePull(env commandEnv, args []string) error {
	opts, pos, err := parsePullArgs(args, env.Stderr)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return fmt.Errorf("usage: cove pull [flags] <ref>")
	}
	if opts.JSON && !opts.DryRun {
		return fmt.Errorf("cove pull: --json requires --dry-run")
	}
	if opts.FetchManifest && !opts.DryRun {
		return fmt.Errorf("cove pull: --fetch-manifest requires --dry-run")
	}
	if opts.FetchManifest && opts.ManifestPath != "" {
		return fmt.Errorf("cove pull: --fetch-manifest cannot be used with --manifest")
	}
	if opts.VerifyBlobs && !opts.DryRun {
		return fmt.Errorf("cove pull: --verify-blobs requires --dry-run")
	}
	if opts.VerifyBlobs && !opts.FetchManifest && opts.ManifestPath == "" {
		return fmt.Errorf("cove pull: --verify-blobs requires --fetch-manifest or --manifest")
	}
	plan, err := buildPullPlan(pos[0], opts)
	if err != nil {
		return err
	}
	if opts.DryRun {
		if opts.JSON {
			return printPullDryRunJSON(env.Stdout, plan)
		}
		printPullDryRun(env.Stdout, plan)
		return nil
	}
	switch plan.Manifest.Format {
	case ociimage.FormatLume:
		if err := lumePullDisk(context.Background(), plan, opts); err != nil {
			return err
		}
	case ociimage.FormatTart:
		if err := tartPullDisk(context.Background(), plan, opts); err != nil {
			return err
		}
	default:
		if err := pullDisk(context.Background(), plan, opts); err != nil {
			return err
		}
	}
	printPullResult(env.Stdout, plan)
	return nil
}

func parsePullArgs(args []string, w io.Writer) (pullOptions, []string, error) {
	var opts pullOptions
	fs := flag.NewFlagSet("pull", flag.ContinueOnError)
	fs.SetOutput(w)
	fs.StringVar(&opts.As, "as", "", "destination VM name")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "validate inputs without writing a disk")
	fs.BoolVar(&opts.JSON, "json", false, "print dry-run plan as JSON")
	fs.BoolVar(&opts.FetchManifest, "fetch-manifest", false, "fetch registry manifest during dry-run")
	fs.BoolVar(&opts.VerifyBlobs, "verify-blobs", false, "HEAD registry blobs during dry-run")
	fs.BoolVar(&opts.Resume, "resume", false, "continue an interrupted pull from disk.img.partial")
	fs.StringVar(&opts.ManifestPath, "manifest", "", "local OCI manifest JSON instead of fetching the registry")
	fs.Usage = func() { printPullUsage(w) }
	if err := fs.Parse(movePullFlagsFirst(args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return opts, nil, nil
		}
		return opts, nil, err
	}
	return opts, fs.Args(), nil
}

func movePullFlagsFirst(args []string) []string {
	return moveKnownFlagsFirst(args, map[string]bool{
		"as":             true,
		"dry-run":        false,
		"json":           false,
		"fetch-manifest": false,
		"verify-blobs":   false,
		"resume":         false,
		"manifest":       true,
	})
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
	vmDirectory := vmconfig.Path(name)
	if err := checkPullTarget(vmDirectory, opts.Resume); err != nil {
		return nil, err
	}

	var (
		parsed         ociimage.ParsedManifest
		manifestDigest string
		manifestRaw    []byte
	)
	if opts.ManifestPath != "" {
		parsed, manifestDigest, err = readPullManifest(opts.ManifestPath)
		if err != nil {
			return nil, err
		}
		manifestRaw, _ = os.ReadFile(opts.ManifestPath)
	} else if opts.DryRun && !opts.FetchManifest && opts.RegistryBaseURL == "" {
		// Keep plain dry runs network-free. Use --manifest when the
		// caller has local manifest JSON, or --fetch-manifest to query
		// registry metadata without pulling disk data.
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), pullManifestFetchTimeout)
		defer cancel()
		parsed, manifestDigest, manifestRaw, err = fetchPullManifest(ctx, ref, opts)
		if err != nil {
			return nil, err
		}
	}
	plan := &pullPlan{
		Ref:            ref,
		VMName:         name,
		VMDir:          vmDirectory,
		Manifest:       parsed,
		ManifestRaw:    manifestRaw,
		ManifestDigest: manifestDigest,
	}
	if opts.DryRun {
		if err := planPullDryRunReuse(plan, opts); err != nil {
			return nil, err
		}
		recordPullDryRunImportBlobs(plan)
		if err := planPullDryRunBlobAudit(context.Background(), plan, opts); err != nil {
			return nil, err
		}
	}
	return plan, nil
}

func checkPullTarget(vmDirectory string, resume bool) error {
	diskPath := filepath.Join(vmDirectory, "disk.img")
	if _, err := os.Stat(diskPath); err == nil {
		if err := ensurePullTargetInactive(vmDirectory); err != nil {
			return err
		}
		return checkIncompletePullDisk(vmDirectory, diskPath, resume)
	} else if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("stat disk: %w", err)
	}
	return checkIncompletePullDisk(vmDirectory, diskPath, resume)
}

func readPullManifest(path string) (ociimage.ParsedManifest, string, error) {
	var out ociimage.ParsedManifest
	data, err := os.ReadFile(path)
	if err != nil {
		return out, "", fmt.Errorf("read manifest: %w", err)
	}
	var manifest ociimage.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return out, "", fmt.Errorf("parse manifest JSON: %w", err)
	}
	out, err = ociimage.ParseManifest(manifest)
	if err != nil {
		return out, "", err
	}
	return out, digestData(data), nil
}

func fetchPullManifest(ctx context.Context, ref ociimage.Reference, opts pullOptions) (ociimage.ParsedManifest, string, []byte, error) {
	var out ociimage.ParsedManifest
	client := pullRegistryClient(ref, opts)
	manifest, digest, err := client.FetchManifest(ctx, ref)
	if err != nil {
		return out, "", nil, err
	}
	out, err = ociimage.ParseManifest(manifest)
	if err != nil {
		return out, "", nil, fmt.Errorf("parse registry manifest: %w", err)
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return out, "", nil, fmt.Errorf("encode registry manifest: %w", err)
	}
	return out, digest, data, nil
}

func recordPullDryRunImportBlobs(plan *pullPlan) {
	if plan == nil || len(plan.FetchBlobDescriptors) > 0 {
		return
	}
	switch plan.Manifest.Format {
	case ociimage.FormatLume:
		for _, part := range plan.Manifest.Lume.DiskParts {
			name := part.Title
			if name == "" {
				name = fmt.Sprintf("disk-part[%d]", part.PartNumber)
			}
			plan.FetchBlobDescriptors = append(plan.FetchBlobDescriptors, pullBlobDescriptor{
				Name:       name,
				Descriptor: part.Descriptor,
			})
		}
		if plan.Manifest.Lume.NvramLayer != nil {
			plan.FetchBlobDescriptors = append(plan.FetchBlobDescriptors, pullBlobDescriptor{
				Name:       "nvram.bin",
				Descriptor: *plan.Manifest.Lume.NvramLayer,
			})
		}
		if plan.Manifest.Lume.ConfigLayer != nil {
			plan.FetchBlobDescriptors = append(plan.FetchBlobDescriptors, pullBlobDescriptor{
				Name:       "config.json",
				Descriptor: *plan.Manifest.Lume.ConfigLayer,
			})
		}
	case ociimage.FormatTart:
		plan.FetchBlobDescriptors = append(plan.FetchBlobDescriptors, pullBlobDescriptor{
			Name:       "nvram",
			Descriptor: plan.Manifest.Tart.NVRAMLayer,
		})
		plan.FetchBlobDescriptors = append(plan.FetchBlobDescriptors, pullBlobDescriptor{
			Name:       "config",
			Descriptor: plan.Manifest.Tart.ConfigLayer,
		})
		for i, layer := range plan.Manifest.Tart.DiskLayers {
			plan.FetchBlobDescriptors = append(plan.FetchBlobDescriptors, pullBlobDescriptor{
				Name:       fmt.Sprintf("disk[%d]", i),
				Descriptor: layer.Descriptor,
			})
		}
	}
}

func planPullDryRunBlobAudit(ctx context.Context, plan *pullPlan, opts pullOptions) error {
	if plan == nil || !opts.VerifyBlobs {
		return nil
	}
	if len(plan.ManifestRaw) == 0 {
		return fmt.Errorf("cove pull: --verify-blobs requires a manifest")
	}
	ctx, cancel := context.WithTimeout(ctx, pullManifestFetchTimeout)
	defer cancel()
	audit, err := auditPullDryRunBlobs(ctx, pullRegistryClient(plan.Ref, opts), plan.Ref, plan.FetchBlobDescriptors)
	if err != nil {
		return fmt.Errorf("cove pull: verify blobs: %w", err)
	}
	plan.BlobAudit = audit.Status
	plan.BlobDescriptors = audit.Checked
	plan.BlobBytes = audit.Bytes
	plan.MissingBlobs = audit.Missing
	return nil
}

func auditPullDryRunBlobs(ctx context.Context, client ociimage.RegistryClient, ref ociimage.Reference, descriptors []pullBlobDescriptor) (remoteBlobAudit, error) {
	audit := remoteBlobAudit{Status: "ok", Checked: len(descriptors)}
	for _, desc := range descriptors {
		if desc.Descriptor.Size > 0 {
			audit.Bytes += desc.Descriptor.Size
		}
		if desc.Descriptor.Digest == "" {
			audit.Missing = append(audit.Missing, desc.Name+":missing digest")
			continue
		}
		ok, err := client.BlobExists(ctx, ref, desc.Descriptor.Digest)
		if err != nil {
			return audit, fmt.Errorf("verify blob %s: %w", desc.Name, err)
		}
		if !ok {
			audit.Missing = append(audit.Missing, desc.Name+":"+desc.Descriptor.Digest)
		}
	}
	if len(audit.Missing) > 0 {
		audit.Status = "missing"
	}
	return audit, nil
}

func pullDisk(ctx context.Context, plan *pullPlan, opts pullOptions) error {
	if plan == nil {
		return fmt.Errorf("cove pull: missing pull plan")
	}
	if len(plan.Manifest.DiskLayers) == 0 {
		return fmt.Errorf("cove pull: manifest has no disk chunks")
	}
	if err := checkPullTarget(plan.VMDir, opts.Resume); err != nil {
		return err
	}
	if err := os.MkdirAll(plan.VMDir, 0755); err != nil {
		return fmt.Errorf("create VM directory: %w", err)
	}
	if err := vmconfig.EnsureCompatibilityAlias(plan.VMName, plan.VMDir); err != nil {
		return fmt.Errorf("create VM compatibility alias: %w", err)
	}
	blobStore := store.New(opts.StoreDir)
	unlock, err := blobStore.LockShared()
	if err != nil {
		return err
	}
	defer unlock()
	if len(plan.ManifestRaw) > 0 {
		if err := blobStore.StoreManifest(plan.ManifestDigest, plan.ManifestRaw); err != nil {
			return err
		}
	}
	baseReuse, err := planPullBaseReuse(plan, blobStore)
	if err != nil {
		return err
	}

	partialPath := filepath.Join(plan.VMDir, "disk.img.partial")
	diskPath := filepath.Join(plan.VMDir, "disk.img")
	disk, baseReuse, zeroExisting, err := createPullPartialDisk(partialPath, plan.Manifest.Annotations.UncompressedDiskSize, baseReuse, opts.Resume)
	if err != nil {
		return err
	}
	recordPullBaseReuse(plan, baseReuse)
	closed := false
	defer func() {
		if !closed {
			disk.Close()
		}
	}()

	client := pullRegistryClient(plan.Ref, opts)
	if err := pullDiskChunks(ctx, client, plan, disk, blobStore, baseReuse, zeroExisting); err != nil {
		return err
	}
	if err := pullMetadataBlobs(ctx, client, plan, blobStore); err != nil {
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

func pullDiskChunks(ctx context.Context, client ociimage.RegistryClient, plan *pullPlan, disk io.WriterAt, blobStore store.Store, baseReuse *pullBaseReuse, zeroExisting bool) error {
	layers, zeroLayers := pullDiskChunkWork(plan.Manifest.DiskLayers, baseReuse, zeroExisting)
	if err := zeroPullDiskChunks(disk, zeroLayers); err != nil {
		return err
	}
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
				if err := pullDiskChunk(ctx, client, plan.Ref, disk, layer, blobStore); err != nil {
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

func pullDiskChunk(ctx context.Context, client ociimage.RegistryClient, ref ociimage.Reference, disk io.WriterAt, layer ociimage.DiskLayer, blobStore store.Store) error {
	err := blobStore.Ensure(ctx, layer.Descriptor.Digest, layer.Descriptor.Size, func(ctx context.Context) (io.ReadCloser, error) {
		return client.FetchBlob(ctx, ref, layer.Descriptor.Digest)
	})
	if err != nil {
		return err
	}
	body, err := blobStore.OpenVerified(layer.Descriptor.Digest, layer.Descriptor.Size)
	if err != nil {
		return err
	}
	defer body.Close()
	if err := ociimage.WriteCompressedChunkAt(disk, layer.Chunk, layer.Descriptor, body); err != nil {
		return err
	}
	return nil
}

func pullMetadataBlobs(ctx context.Context, client ociimage.RegistryClient, plan *pullPlan, blobStore store.Store) error {
	for _, desc := range plan.Manifest.Blobs {
		name, ok, err := pullMetadataFileName(desc)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		path := filepath.Join(plan.VMDir, name)
		if err := pullBlobToFile(ctx, client, plan.Ref, desc, path, blobStore); err != nil {
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

func pullBlobToFile(ctx context.Context, client ociimage.RegistryClient, ref ociimage.Reference, desc ociimage.Descriptor, path string, blobStore store.Store) error {
	if desc.Digest == "" {
		return fmt.Errorf("pull metadata blob: missing digest")
	}
	if desc.Size < 0 {
		return fmt.Errorf("pull metadata blob: negative size %d", desc.Size)
	}
	if err := blobStore.Ensure(ctx, desc.Digest, desc.Size, func(ctx context.Context) (io.ReadCloser, error) {
		return client.FetchBlob(ctx, ref, desc.Digest)
	}); err != nil {
		return err
	}
	body, err := blobStore.OpenVerified(desc.Digest, desc.Size)
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

func digestData(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
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
	switch plan.Manifest.Format {
	case ociimage.FormatLume:
		fmt.Fprintf(w, "  format: lume (tar-split)\n")
		fmt.Fprintf(w, "  disk parts: %d\n", len(plan.Manifest.Lume.DiskParts))
		var compressed int64
		for _, p := range plan.Manifest.Lume.DiskParts {
			compressed += p.Descriptor.Size
		}
		fmt.Fprintf(w, "  compressed bytes: %s\n", bytefmt.Size(compressed))
		if plan.Manifest.Lume.NvramLayer != nil {
			fmt.Fprintf(w, "  nvram.bin: %s\n", bytefmt.Size(plan.Manifest.Lume.NvramLayer.Size))
		}
		if plan.Manifest.Lume.ConfigLayer != nil {
			fmt.Fprintf(w, "  config.json: %s\n", bytefmt.Size(plan.Manifest.Lume.ConfigLayer.Size))
		}
		printPullBlobAudit(w, plan)
		return
	case ociimage.FormatTart:
		fmt.Fprintf(w, "  format: tart (apple-lz4)\n")
		fmt.Fprintf(w, "  disk size: %s\n", bytefmt.Size(plan.Manifest.Tart.UncompressedDiskSize))
		fmt.Fprintf(w, "  disk layers: %d\n", len(plan.Manifest.Tart.DiskLayers))
		var compressed int64
		for _, l := range plan.Manifest.Tart.DiskLayers {
			compressed += l.Descriptor.Size
		}
		fmt.Fprintf(w, "  compressed bytes: %s\n", bytefmt.Size(compressed))
		fmt.Fprintf(w, "  nvram: %s\n", bytefmt.Size(plan.Manifest.Tart.NVRAMLayer.Size))
		fmt.Fprintf(w, "  config: %s\n", bytefmt.Size(plan.Manifest.Tart.ConfigLayer.Size))
		if plan.Manifest.Tart.UploadTime != "" {
			fmt.Fprintf(w, "  upload time: %s\n", plan.Manifest.Tart.UploadTime)
		}
		printPullBlobAudit(w, plan)
		return
	}
	if len(plan.Manifest.Chunks) == 0 && plan.Manifest.Annotations.UncompressedDiskSize == 0 {
		fmt.Fprintln(w, "  manifest: not provided")
		return
	}
	fmt.Fprintf(w, "  format: cove\n")
	fmt.Fprintf(w, "  disk size: %s\n", bytefmt.Size(plan.Manifest.Annotations.UncompressedDiskSize))
	if plan.Manifest.Annotations.DiskFormat != "" {
		fmt.Fprintf(w, "  disk format: %s\n", plan.Manifest.Annotations.DiskFormat)
	}
	fmt.Fprintf(w, "  chunks: %d\n", len(plan.Manifest.Chunks))
	fmt.Fprintf(w, "  metadata blobs: %d\n", len(plan.Manifest.Blobs))
	printPullDryRunTransfer(w, plan)
	printPullBlobAudit(w, plan)
	printPullBaseReuse(w, plan)
}

func printPullDryRunJSON(w io.Writer, plan *pullPlan) error {
	data, err := json.MarshalIndent(pullDryRunOutputFromPlan(plan), "", "  ")
	if err != nil {
		return err
	}
	if _, err := w.Write(append(data, '\n')); err != nil {
		return err
	}
	return nil
}

func pullDryRunOutputFromPlan(plan *pullPlan) pullDryRunOutput {
	out := pullDryRunOutput{
		Ref:              plan.Ref.String(),
		VM:               plan.VMName,
		Target:           plan.VMDir,
		ManifestProvided: pullPlanHasManifest(plan),
		ManifestDigest:   plan.ManifestDigest,
	}
	if !out.ManifestProvided {
		return out
	}
	switch plan.Manifest.Format {
	case ociimage.FormatLume:
		out.Format = "lume"
		out.DiskParts = len(plan.Manifest.Lume.DiskParts)
		for _, p := range plan.Manifest.Lume.DiskParts {
			out.CompressedBytes += p.Descriptor.Size
		}
		if plan.Manifest.Lume.NvramLayer != nil {
			out.NVRAMBytes = plan.Manifest.Lume.NvramLayer.Size
		}
		if plan.Manifest.Lume.ConfigLayer != nil {
			out.ConfigBytes = plan.Manifest.Lume.ConfigLayer.Size
		}
	case ociimage.FormatTart:
		out.Format = "tart"
		out.DiskSize = plan.Manifest.Tart.UncompressedDiskSize
		out.DiskLayers = len(plan.Manifest.Tart.DiskLayers)
		for _, l := range plan.Manifest.Tart.DiskLayers {
			out.CompressedBytes += l.Descriptor.Size
		}
		out.NVRAMBytes = plan.Manifest.Tart.NVRAMLayer.Size
		out.ConfigBytes = plan.Manifest.Tart.ConfigLayer.Size
		out.UploadTime = plan.Manifest.Tart.UploadTime
	default:
		out.Format = "cove"
		out.DiskSize = plan.Manifest.Annotations.UncompressedDiskSize
		out.DiskFormat = plan.Manifest.Annotations.DiskFormat
		out.Chunks = len(plan.Manifest.Chunks)
		out.MetadataBlobs = len(plan.Manifest.Blobs)
		if len(plan.Manifest.DiskLayers) > 0 {
			out.Transfer = &pullDryRunTransferOutput{
				DiskFetchChunks:    plan.FetchDiskChunks,
				DiskFetchBytes:     plan.FetchDiskBytes,
				DiskStoreChunks:    plan.StoreDiskChunks,
				DiskStoreBytes:     plan.StoreDiskBytes,
				ZeroChunks:         plan.ZeroDiskChunks,
				ZeroBytes:          plan.ZeroDiskBytes,
				MetadataFetchBlobs: plan.FetchMetadataBlobs,
				MetadataFetchBytes: plan.FetchMetadataBytes,
				MetadataStoreBlobs: plan.StoreMetadataBlobs,
				MetadataStoreBytes: plan.StoreMetadataBytes,
			}
		}
	}
	if plan.BaseReuseChunks > 0 {
		out.BaseReuse = &pullBaseReuseOutput{
			Path:       plan.BaseReusePath,
			DiskFormat: plan.BaseReuseDiskFormat,
			Chunks:     plan.BaseReuseChunks,
			Bytes:      plan.BaseReuseBytes,
		}
	}
	if plan.BlobAudit != "" {
		out.BlobAudit = &pullBlobAuditOutput{
			Status:      plan.BlobAudit,
			Descriptors: plan.BlobDescriptors,
			Bytes:       plan.BlobBytes,
			Missing:     append([]string(nil), plan.MissingBlobs...),
		}
	}
	return out
}

func pullPlanHasManifest(plan *pullPlan) bool {
	return plan.ManifestDigest != "" ||
		len(plan.ManifestRaw) > 0 ||
		plan.Manifest.Format != ociimage.FormatCove ||
		len(plan.Manifest.Chunks) > 0 ||
		plan.Manifest.Annotations.UncompressedDiskSize != 0
}

func printPullResult(w io.Writer, plan *pullPlan) {
	fmt.Fprintln(w, "Pull complete")
	fmt.Fprintf(w, "  ref: %s\n", plan.Ref.String())
	fmt.Fprintf(w, "  vm: %s\n", plan.VMName)
	fmt.Fprintf(w, "  target: %s\n", plan.VMDir)
	printPullBaseReuse(w, plan)
}

func printPullBaseReuse(w io.Writer, plan *pullPlan) {
	if plan.BaseReuseChunks > 0 {
		fmt.Fprintf(w, "  base reuse: %d chunks (%s)", plan.BaseReuseChunks, bytefmt.Size(plan.BaseReuseBytes))
		if plan.BaseReuseDiskFormat != "" {
			fmt.Fprintf(w, " format=%s", plan.BaseReuseDiskFormat)
		}
		if plan.BaseReusePath != "" {
			fmt.Fprintf(w, " from=%s", plan.BaseReusePath)
		}
		fmt.Fprintln(w)
	}
}

func printPullDryRunTransfer(w io.Writer, plan *pullPlan) {
	if plan.FetchDiskChunks > 0 {
		fmt.Fprintf(w, "  disk fetch: %d chunks (%s compressed)\n", plan.FetchDiskChunks, bytefmt.Size(plan.FetchDiskBytes))
	}
	if plan.StoreDiskChunks > 0 {
		fmt.Fprintf(w, "  disk store reuse: %d chunks (%s compressed)\n", plan.StoreDiskChunks, bytefmt.Size(plan.StoreDiskBytes))
	}
	if plan.ZeroDiskChunks > 0 {
		fmt.Fprintf(w, "  zero chunks: %d (%s)\n", plan.ZeroDiskChunks, bytefmt.Size(plan.ZeroDiskBytes))
	}
	if plan.FetchMetadataBlobs > 0 {
		fmt.Fprintf(w, "  metadata fetch: %d blobs (%s)\n", plan.FetchMetadataBlobs, bytefmt.Size(plan.FetchMetadataBytes))
	}
	if plan.StoreMetadataBlobs > 0 {
		fmt.Fprintf(w, "  metadata store reuse: %d blobs (%s)\n", plan.StoreMetadataBlobs, bytefmt.Size(plan.StoreMetadataBytes))
	}
}

func printPullBlobAudit(w io.Writer, plan *pullPlan) {
	if plan.BlobAudit == "" {
		return
	}
	fmt.Fprintf(w, "  blob audit: %s", plan.BlobAudit)
	if plan.BlobDescriptors > 0 {
		fmt.Fprintf(w, " (%d descriptors", plan.BlobDescriptors)
		if plan.BlobBytes > 0 {
			fmt.Fprintf(w, ", %s)", bytefmt.Size(plan.BlobBytes))
		} else {
			fmt.Fprint(w, ")")
		}
	}
	fmt.Fprintln(w)
	for _, missing := range plan.MissingBlobs {
		fmt.Fprintf(w, "    missing: %s\n", missing)
	}
}

func printPullUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove pull [flags] <ref>

Validate or pull an OCI VM image.

Pull fetches the registry manifest, streams verified LZ4 disk chunks into
disk.img.partial, restores macOS identity metadata, and atomically renames the
verified disk into place. Use --dry-run to validate the manifest and target
without writing a disk.

Flags:
  --as <name>          Destination VM name
  --dry-run            Validate inputs without writing a disk
  --json               Print dry-run plan as JSON
  --fetch-manifest     Fetch registry manifest during dry-run
  --verify-blobs       HEAD registry blobs during dry-run
  --resume             Continue an interrupted pull from disk.img.partial
  --manifest <path>    Local OCI manifest JSON instead of fetching the registry`)
}
