package main

import (
	"bytes"
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

	"github.com/tmc/vz-macos/internal/bytefmt"
	"github.com/tmc/vz-macos/internal/ociimage"
	"github.com/tmc/vz-macos/internal/vmconfig"
)

type pushOptions struct {
	BaseRef         string
	ChunkSize       int64
	DryRun          bool
	LumeCompat      bool
	Format          string
	AdditionalTags  stringList
	ManifestOut     string
	RegistryBaseURL string
	RegistryToken   string
}

type pushPlan struct {
	VMName      string
	VMDir       string
	DiskPath    string
	Ref         string
	BaseRef     string
	BaseSource  ociimage.Reference
	BaseLayers  []bool
	BaseChunks  int
	BaseBytes   int64
	ChunkSize   int64
	DiskSize    int64
	Chunks      []ociimage.Chunk
	ZeroChunks  int
	ZeroBytes   int64
	LumeCompat  bool
	ExtraTags   []string
	ManifestOut string
	Blobs       []ociimage.Blob
	Prepared    []ociimage.PreparedChunk
	Manifest    ociimage.Manifest
	ConfigJSON  []byte
}

type stringList []string

func (l *stringList) String() string {
	return strings.Join(*l, ",")
}

func (l *stringList) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("empty value")
	}
	*l = append(*l, value)
	return nil
}

func handlePush(args []string) error {
	opts, pos, err := parsePushArgs(args, os.Stderr)
	if err != nil {
		return err
	}
	if len(pos) != 2 {
		return fmt.Errorf("usage: cove push <vm> <ref> [flags]")
	}
	switch opts.Format {
	case "", "cove":
		// fall through
	case "lume":
		lumePlan, err := buildLumePushPlan(pos[0], pos[1], opts)
		if err != nil {
			return err
		}
		return lumePushDryRunOnly(lumePlan, opts)
	default:
		return fmt.Errorf("unknown push format %q (want cove or lume)", opts.Format)
	}
	plan, err := buildPushPlan(pos[0], pos[1], opts)
	if err != nil {
		return err
	}
	if opts.ManifestOut != "" {
		if opts.DryRun {
			if err := writePushManifest(opts.ManifestOut, plan.Manifest); err != nil {
				return err
			}
		}
	}
	if !opts.DryRun {
		if err := pushImage(context.Background(), plan, opts); err != nil {
			return err
		}
		if opts.ManifestOut != "" {
			if err := writePushManifest(opts.ManifestOut, plan.Manifest); err != nil {
				return err
			}
		}
		printPushResult(os.Stdout, plan)
		return nil
	}
	printPushDryRun(os.Stdout, plan)
	return nil
}

func parsePushArgs(args []string, w io.Writer) (pushOptions, []string, error) {
	opts := pushOptions{ChunkSize: ociimage.DefaultChunkSize}
	fs := flag.NewFlagSet("push", flag.ContinueOnError)
	fs.SetOutput(w)
	fs.StringVar(&opts.BaseRef, "base", "", "base image for delta push")
	chunkSizeMB := fs.Int64("chunk-size", opts.ChunkSize>>20, "chunk size in megabytes")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "print the plan without uploading")
	fs.BoolVar(&opts.LumeCompat, "lume-compat", false, "emit dual cove and lume annotations")
	fs.StringVar(&opts.Format, "format", "cove", "output image format: cove (default) or lume (tar-split, dry-run only)")
	fs.Var(&opts.AdditionalTags, "additional-tag", "additional tag to publish")
	fs.StringVar(&opts.ManifestOut, "manifest-out", "", "write OCI manifest JSON to path")
	fs.Usage = func() { printPushUsage(w) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return opts, nil, nil
		}
		return opts, nil, err
	}
	if *chunkSizeMB <= 0 {
		return opts, nil, fmt.Errorf("invalid chunk size %d MB", *chunkSizeMB)
	}
	opts.ChunkSize = *chunkSizeMB << 20
	return opts, fs.Args(), nil
}

func buildPushPlan(vmName, ref string, opts pushOptions) (*pushPlan, error) {
	if opts.ChunkSize <= 0 {
		return nil, fmt.Errorf("invalid chunk size %d", opts.ChunkSize)
	}
	if err := validatePushReferences(ref, opts); err != nil {
		return nil, err
	}
	vmDirectory := pushSourceDir(vmName)
	if !vmconfig.Validate(vmDirectory) {
		return nil, fmt.Errorf("vm not found or invalid: %s", vmDirectory)
	}
	if err := ensurePushSourceInactive(vmDirectory); err != nil {
		return nil, err
	}
	diskPath, err := pushDiskPath(vmDirectory)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(diskPath)
	if err != nil {
		return nil, fmt.Errorf("open disk: %w", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat disk: %w", err)
	}
	chunks, err := ociimage.DescribeChunks(f, opts.ChunkSize)
	if err != nil {
		return nil, err
	}
	blobs, err := pushMetadataBlobs(vmDirectory, diskPath)
	if err != nil {
		return nil, err
	}
	prepared, descriptors, err := preparePushChunkLayers(f, chunks, opts.LumeCompat)
	if err != nil {
		return nil, err
	}
	manifest, configJSON, err := ociimage.BuildManifest(ociimage.ManifestOptions{
		UploadTime:       time.Now().UTC().Format(time.RFC3339),
		DiskSize:         info.Size(),
		Chunks:           chunks,
		ChunkDescriptors: descriptors,
		Blobs:            blobs,
		LumeCompat:       opts.LumeCompat,
	})
	if err != nil {
		return nil, err
	}
	plan := &pushPlan{
		VMName:      vmName,
		VMDir:       vmDirectory,
		DiskPath:    diskPath,
		Ref:         ref,
		BaseRef:     opts.BaseRef,
		ChunkSize:   opts.ChunkSize,
		DiskSize:    info.Size(),
		Chunks:      chunks,
		LumeCompat:  opts.LumeCompat,
		ExtraTags:   append([]string(nil), opts.AdditionalTags...),
		ManifestOut: opts.ManifestOut,
		Blobs:       blobs,
		Prepared:    prepared,
		Manifest:    manifest,
		ConfigJSON:  configJSON,
	}
	for _, c := range chunks {
		if c.Zero {
			plan.ZeroChunks++
			plan.ZeroBytes += c.Size
		}
	}
	return plan, nil
}

func pushSourceDir(name string) string {
	if path, ok := localVMDirectory(name); ok {
		return path
	}
	return vmconfig.Path(name)
}

func localVMDirectory(name string) (string, bool) {
	path := expandHome(name)
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() || !vmconfig.Validate(path) {
		return "", false
	}
	return resolvePath(path), true
}

func preparePushChunkLayers(r io.ReaderAt, chunks []ociimage.Chunk, lumeCompat bool) ([]ociimage.PreparedChunk, []ociimage.Descriptor, error) {
	prepared := make([]ociimage.PreparedChunk, len(chunks))
	descriptors := make([]ociimage.Descriptor, len(chunks))
	for i, chunk := range chunks {
		p, err := ociimage.PrepareChunkLayer(r, chunk, len(chunks), lumeCompat)
		if err != nil {
			return nil, nil, err
		}
		prepared[i] = p
		if p.SkipUpload {
			descriptors[i] = ociimage.Descriptor{
				MediaType: ociimage.MediaTypeLayer,
				Size:      0,
				Digest:    chunk.Digest,
			}
			continue
		}
		descriptors[i] = p.Descriptor
	}
	return prepared, descriptors, nil
}

func pushImage(ctx context.Context, plan *pushPlan, opts pushOptions) error {
	ref, err := ociimage.ParseReference(plan.Ref)
	if err != nil {
		return fmt.Errorf("cove push: invalid target ref %q: %w", plan.Ref, err)
	}
	client := pushRegistryClient(ref, opts)
	if plan.BaseRef != "" {
		if err := applyPushBase(ctx, plan, ref, opts); err != nil {
			return err
		}
	}
	if err := uploadBytesBlob(ctx, client, ref, plan.Manifest.Config, plan.ConfigJSON); err != nil {
		return err
	}
	for _, blob := range plan.Blobs {
		name, ok := pushMetadataFileName(blob.Role)
		if !ok {
			continue
		}
		desc := ociimage.Descriptor{MediaType: ociimage.MediaTypeLayer, Size: blob.Size, Digest: blob.Digest}
		if err := uploadFileBlob(ctx, client, ref, desc, filepath.Join(plan.VMDir, name)); err != nil {
			return err
		}
	}
	for i, chunk := range plan.Prepared {
		if chunk.SkipUpload {
			if !plan.usesBaseLayer(i) {
				continue
			}
			reused, err := ensurePushBaseBlob(ctx, client, ref, plan.BaseSource, chunk.Descriptor)
			if err != nil {
				return err
			}
			if reused {
				continue
			}
		}
		if err := uploadBytesBlob(ctx, client, ref, chunk.Descriptor, chunk.Data); err != nil {
			return err
		}
	}
	if _, err := client.PushManifest(ctx, ref, plan.Manifest); err != nil {
		return err
	}
	for _, tag := range plan.ExtraTags {
		extra := ref
		extra.Tag = tag
		if _, err := client.PushManifest(ctx, extra, plan.Manifest); err != nil {
			return err
		}
	}
	return nil
}

func (plan *pushPlan) usesBaseLayer(i int) bool {
	return i >= 0 && i < len(plan.BaseLayers) && plan.BaseLayers[i]
}

func ensurePushBaseBlob(ctx context.Context, client ociimage.RegistryClient, target ociimage.Reference, source ociimage.Reference, desc ociimage.Descriptor) (bool, error) {
	if target.Repository == source.Repository {
		exists, err := client.BlobExists(ctx, target, desc.Digest)
		if err != nil {
			return false, err
		}
		return exists, nil
	}
	return client.MountBlob(ctx, target, source, desc)
}

func applyPushBase(ctx context.Context, plan *pushPlan, target ociimage.Reference, opts pushOptions) error {
	base, err := ociimage.ParseReference(plan.BaseRef)
	if err != nil {
		return fmt.Errorf("cove push: invalid base ref %q: %w", plan.BaseRef, err)
	}
	client := pushRegistryClient(base, opts)
	manifest, digest, err := client.FetchManifest(ctx, base)
	if err != nil {
		return err
	}
	parsed, err := ociimage.ParseManifest(manifest)
	if err != nil {
		return fmt.Errorf("parse base manifest: %w", err)
	}
	if parsed.Annotations.UncompressedDiskSize != plan.DiskSize {
		return fmt.Errorf("cove push: base disk size %d, want %d", parsed.Annotations.UncompressedDiskSize, plan.DiskSize)
	}
	if len(parsed.DiskLayers) != len(plan.Prepared) {
		return fmt.Errorf("cove push: base chunks %d, want %d", len(parsed.DiskLayers), len(plan.Prepared))
	}
	if plan.Manifest.Annotations == nil {
		plan.Manifest.Annotations = map[string]string{}
	}
	plan.Manifest.Annotations[ociimage.CoveBaseManifest] = digest
	if base.Registry != target.Registry {
		return nil
	}
	plan.BaseSource = base
	plan.BaseLayers = make([]bool, len(plan.Prepared))
	for i, chunk := range plan.Prepared {
		if chunk.SkipUpload || i >= len(parsed.DiskLayers) {
			continue
		}
		baseLayer := parsed.DiskLayers[i]
		if !samePushBaseChunk(chunk.Chunk, baseLayer.Chunk) || !samePushBaseDescriptor(chunk.Descriptor, baseLayer.Descriptor) {
			continue
		}
		plan.Prepared[i].SkipUpload = true
		plan.BaseLayers[i] = true
		plan.BaseChunks++
		plan.BaseBytes += chunk.Chunk.Size
	}
	return nil
}

func samePushBaseChunk(a, b ociimage.Chunk) bool {
	return a.Index == b.Index && a.Offset == b.Offset && a.Size == b.Size && a.Digest == b.Digest
}

func samePushBaseDescriptor(a, b ociimage.Descriptor) bool {
	return a.MediaType == b.MediaType && a.Size == b.Size && a.Digest == b.Digest
}

func pushRegistryClient(ref ociimage.Reference, opts pushOptions) ociimage.RegistryClient {
	return ociimage.RegistryClient{
		BaseURL:       opts.RegistryBaseURL,
		Authorization: registryAuthorization(ref, opts.RegistryToken),
		TokenCache:    ociimage.NewRegistryTokenCache(),
	}
}

func uploadBytesBlob(ctx context.Context, client ociimage.RegistryClient, ref ociimage.Reference, desc ociimage.Descriptor, data []byte) error {
	exists, err := client.BlobExists(ctx, ref, desc.Digest)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return client.UploadBlob(ctx, ref, desc, bytes.NewReader(data))
}

func uploadFileBlob(ctx context.Context, client ociimage.RegistryClient, ref ociimage.Reference, desc ociimage.Descriptor, path string) error {
	exists, err := client.BlobExists(ctx, ref, desc.Digest)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open blob: %w", err)
	}
	defer f.Close()
	return client.UploadBlob(ctx, ref, desc, f)
}

func pushMetadataFileName(role string) (string, bool) {
	switch role {
	case "nvram":
		return "aux.img", true
	case "hw-model":
		return "hw.model", true
	case "machine-id":
		return "machine.id", true
	default:
		return "", false
	}
}

func writePushManifest(path string, manifest ociimage.Manifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

func pushMetadataBlobs(vmDirectory, diskPath string) ([]ociimage.Blob, error) {
	if filepath.Base(diskPath) != "disk.img" {
		return nil, nil
	}
	specs := []struct {
		name string
		role string
	}{
		{name: "aux.img", role: "nvram"},
		{name: "hw.model", role: "hw-model"},
		{name: "machine.id", role: "machine-id"},
	}
	blobs := make([]ociimage.Blob, 0, len(specs))
	for _, spec := range specs {
		blob, err := ociimage.DigestFile(filepath.Join(vmDirectory, spec.name))
		if err != nil {
			return nil, fmt.Errorf("macOS push requires %s: %w", spec.name, err)
		}
		blob.Role = spec.role
		blobs = append(blobs, blob)
	}
	return blobs, nil
}

func validatePushReferences(ref string, opts pushOptions) error {
	target, err := ociimage.ParseReference(ref)
	if err != nil {
		return fmt.Errorf("cove push: invalid target ref %q: %w", ref, err)
	}
	if target.Tag == "" {
		return fmt.Errorf("cove push: target ref %q must include a tag", ref)
	}
	if target.Digest != "" {
		return fmt.Errorf("cove push: target ref %q must not include a digest", ref)
	}
	if opts.BaseRef != "" {
		base, err := ociimage.ParseReference(opts.BaseRef)
		if err != nil {
			return fmt.Errorf("cove push: invalid base ref %q: %w", opts.BaseRef, err)
		}
		if base.Tag == "" && base.Digest == "" {
			return fmt.Errorf("cove push: base ref %q must include a tag or digest", opts.BaseRef)
		}
	}
	for _, tag := range opts.AdditionalTags {
		if err := ociimage.ValidateTag(tag); err != nil {
			return fmt.Errorf("cove push: invalid additional tag %q: %w", tag, err)
		}
	}
	return nil
}

func ensurePushSourceInactive(vmDirectory string) error {
	active, err := probeControlSocket(GetControlSocketPathForVM(vmDirectory), pullTargetProbeTimeout)
	if err != nil {
		return err
	}
	if active {
		name := filepath.Base(vmDirectory)
		return fmt.Errorf("cove push: cannot push an active VM %q. Stop the VM first: cove ctl stop %s", name, name)
	}
	return nil
}

func pushDiskPath(vmDirectory string) (string, error) {
	for _, name := range []string{"disk.img", "linux-disk.img"} {
		path := filepath.Join(vmDirectory, name)
		if _, err := os.Stat(path); err == nil {
			return path, nil
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("stat disk: %w", err)
		}
	}
	return "", fmt.Errorf("disk image not found in %s", vmDirectory)
}

func printPushResult(w io.Writer, plan *pushPlan) {
	fmt.Fprintln(w, "Push complete")
	fmt.Fprintf(w, "  vm: %s\n", plan.VMName)
	fmt.Fprintf(w, "  ref: %s\n", plan.Ref)
	if plan.BaseRef != "" {
		fmt.Fprintf(w, "  base: %s\n", plan.BaseRef)
		fmt.Fprintf(w, "  base chunks reused: %d (%s)\n", plan.BaseChunks, bytefmt.Size(plan.BaseBytes))
	}
	if len(plan.ExtraTags) > 0 {
		fmt.Fprintf(w, "  additional tags: %s\n", strings.Join(plan.ExtraTags, ", "))
	}
}

func printPushDryRun(w io.Writer, plan *pushPlan) {
	fmt.Fprintln(w, "Push dry run")
	fmt.Fprintf(w, "  vm: %s\n", plan.VMName)
	fmt.Fprintf(w, "  ref: %s\n", plan.Ref)
	fmt.Fprintf(w, "  disk: %s\n", plan.DiskPath)
	fmt.Fprintf(w, "  disk size: %s\n", bytefmt.Size(plan.DiskSize))
	fmt.Fprintf(w, "  chunk size: %s\n", bytefmt.Size(plan.ChunkSize))
	fmt.Fprintf(w, "  chunks: %d\n", len(plan.Chunks))
	fmt.Fprintf(w, "  zero chunks: %d (%s)\n", plan.ZeroChunks, bytefmt.Size(plan.ZeroBytes))
	fmt.Fprintf(w, "  non-zero bytes: %s\n", bytefmt.Size(plan.DiskSize-plan.ZeroBytes))
	if plan.BaseRef != "" {
		fmt.Fprintf(w, "  base: %s (not fetched in dry-run)\n", plan.BaseRef)
	}
	if len(plan.ExtraTags) > 0 {
		fmt.Fprintf(w, "  additional tags: %s\n", strings.Join(plan.ExtraTags, ", "))
	}
	if plan.LumeCompat {
		fmt.Fprintln(w, "  lume compat: yes")
	}
}

func printPushUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove push <vm|dir> <ref> [flags]

Plan or push a VM disk as an OCI image.

Push compresses non-zero disk chunks as LZ4 OCI layers, skips sparse zero
chunks, uploads missing blobs, and publishes the manifest tag. With --base,
push fetches the base manifest and reuses matching chunk blobs from the same
registry. The source can be a VM name or an existing VM directory. Use --dry-run
to inspect the local chunk plan without network access.

Flags:
  --base <ref>              Base image for delta push
  --chunk-size <mb>         Chunk size in megabytes (default 512)
  --dry-run                 Print the chunk plan without uploading
  --lume-compat             Plan dual cove and lume annotations
  --format <fmt>            Output format: cove (default) or lume (tar-split, --dry-run only)
  --additional-tag <tag>    Additional tag to publish (repeatable)
  --manifest-out <path>     Write OCI manifest JSON to path`)
}
