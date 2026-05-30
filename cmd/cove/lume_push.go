// lume_push.go - Export cove VMs as lume tar-split OCI artifacts.
//
// Cove publishes disk images as LZ4-compressed chunks addressed by an
// org.tmc.cove.chunk-index annotation. Lume publishes them as a single
// tar-gzipped stream sliced byte-wise into N "disk.img.part.aa..bo" layers.
// The two formats disagree on compression, addressing, and verification, so
// instead of merging them we build the lume manifest in a parallel module.
package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tmc/cove/internal/bytefmt"
	"github.com/tmc/cove/internal/ociimage"
	"github.com/tmc/cove/internal/vmconfig"
)

// lumeDefaultChunkSize matches what trycua publishes on ghcr.io: 500 MiB
// per part for all but the last, which is whatever's left.
const lumeDefaultChunkSize = 500 << 20

// lumePartTitle returns the title for the n-th part (1-based).
// Encoded as a two-letter base-26 sequence: aa=1, ab=2, ..., az=26, ba=27, ...
// Lume's published images use 41 parts addressed aa..bo.
func lumePartTitle(n int) string {
	if n < 1 {
		return ""
	}
	idx := n - 1
	first := byte('a' + idx/26)
	second := byte('a' + idx%26)
	return fmt.Sprintf("%s%c%c", ociimage.LumeDiskPartPrefix, first, second)
}

// lumeConfigOut is the on-disk JSON schema lume publishes. Field names are
// camelCase and the values match the trycua/ubuntu-noble-vanilla:latest
// reference image: cpuCount (int), memorySize (bytes), diskSize (bytes),
// os ("linux" or "macos"), display ("WIDTHxHEIGHT"), macAddress.
//
// This is intentionally a separate type from ociimage.LumeConfig (the import
// decoder) — the export schema is what lume's ghcr.io images actually carry,
// while the import decoder accepts a looser shape. See
// docs/research/cove-lume-export.md for the field-by-field projection.
type lumeConfigOut struct {
	OS         string `json:"os"`
	CPUCount   int    `json:"cpuCount"`
	MemorySize uint64 `json:"memorySize"`
	DiskSize   uint64 `json:"diskSize"`
	Display    string `json:"display,omitempty"`
	MACAddress string `json:"macAddress,omitempty"`
}

// lumePushPlan is the plan for a cove-to-lume export.
type lumePushPlan struct {
	VMName      string
	VMDir       string
	Ref         string
	DiskPath    string
	DiskSize    int64
	ChunkSize   int64
	Config      lumeConfigOut
	ConfigJSON  []byte
	NvramPath   string
	NvramDigest string
	NvramSize   int64
	Parts       []lumePushPart
	Manifest    ociimage.Manifest
	UploadTime  string
	StreamSize  int64 // total compressed bytes (sum of parts)
}

// lumePushPart is one part.aa..bo descriptor in the dry-run plan.
type lumePushPart struct {
	Number    int
	Title     string
	Size      int64
	Digest    string
	MediaType string
}

// buildLumePushPlan constructs a lume export plan for the named VM.
// The plan includes the manifest, sidecar digests, and per-part metadata
// (number, title, size, digest, mediaType).
//
// The disk is tar+gzipped to a temp file, split into chunkSize byte slices,
// and each slice is sha256'd. We use a temp file rather than streaming
// because the manifest must reference per-part sizes/digests up front, and
// the gzipped tar stream's total size isn't known until it's written.
func buildLumePushPlan(vmName, ref string, opts pushOptions) (*lumePushPlan, error) {
	if err := validatePushReferences(ref, opts); err != nil {
		return nil, err
	}
	if opts.BaseRef != "" {
		return nil, fmt.Errorf("cove push --format lume does not support --base")
	}
	vmDir := pushSourceDir(vmName)
	if !vmconfig.Validate(vmDir) {
		return nil, fmt.Errorf("vm not found or invalid: %s", vmDir)
	}
	if err := ensurePushSourceInactive(vmDir); err != nil {
		return nil, err
	}
	diskPath, err := pushDiskPath(vmDir)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(diskPath)
	if err != nil {
		return nil, fmt.Errorf("stat disk: %w", err)
	}

	chunkSize := opts.ChunkSize
	if chunkSize <= 0 {
		chunkSize = lumeDefaultChunkSize
	}

	cfg, err := projectCoveToLume(vmDir, info.Size())
	if err != nil {
		return nil, err
	}
	configJSON, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal lume config: %w", err)
	}
	configJSON = append(configJSON, '\n')

	// The nvram sidecar maps to cove's aux.img. Lume requires it for boot.
	nvramPath := filepath.Join(vmDir, "aux.img")
	nvramBlob, err := ociimage.DigestFile(nvramPath)
	if err != nil {
		return nil, fmt.Errorf("digest nvram: %w", err)
	}

	parts, totalCompressed, err := planLumeDiskParts(diskPath, chunkSize)
	if err != nil {
		return nil, err
	}

	uploadTime := time.Now().UTC().Format(time.RFC3339)
	plan := &lumePushPlan{
		VMName:      vmName,
		VMDir:       vmDir,
		Ref:         ref,
		DiskPath:    diskPath,
		DiskSize:    info.Size(),
		ChunkSize:   chunkSize,
		Config:      cfg,
		ConfigJSON:  configJSON,
		NvramPath:   nvramPath,
		NvramDigest: nvramBlob.Digest,
		NvramSize:   nvramBlob.Size,
		Parts:       parts,
		UploadTime:  uploadTime,
		StreamSize:  totalCompressed,
	}
	plan.Manifest = buildLumeManifest(plan, configJSON)
	return plan, nil
}

// projectCoveToLume reads the VM's cove config + identity files and emits
// the lume config schema. Missing fields fall back to lume's defaults
// (4 CPUs, 4 GiB memory, 1024x768 display, OS guessed from disk name).
func projectCoveToLume(vmDir string, diskSize int64) (lumeConfigOut, error) {
	cfg, err := vmconfig.Load(vmDir)
	if err != nil {
		return lumeConfigOut{}, err
	}
	out := lumeConfigOut{
		OS:         lumeGuessOS(vmDir),
		CPUCount:   4,
		MemorySize: 4 << 30,
		DiskSize:   uint64(diskSize),
		Display:    "1024x768",
	}
	if cfg != nil {
		if cfg.CPU > 0 {
			out.CPUCount = int(cfg.CPU)
		}
		if cfg.MemoryGB > 0 {
			out.MemorySize = uint64(cfg.MemoryGB) << 30
		}
	}
	if mac, ok := readMACAddress(vmDir); ok {
		out.MACAddress = mac
	}
	return out, nil
}

func lumeGuessOS(vmDir string) string {
	if _, err := os.Stat(filepath.Join(vmDir, "linux-disk.img")); err == nil {
		return "linux"
	}
	return "macos"
}

func readMACAddress(vmDir string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(vmDir, "mac.address"))
	if err != nil {
		return "", false
	}
	mac := strings.TrimSpace(string(data))
	if _, err := net.ParseMAC(mac); err != nil {
		return "", false
	}
	return mac, true
}

// planLumeDiskParts tars+gzips the disk, slices the stream into chunkSize
// byte ranges, and returns one descriptor per part plus the total compressed
// byte count. The tar stream is buffered in a temp file because the manifest
// must list every part's size and digest before any upload.
func planLumeDiskParts(diskPath string, chunkSize int64) ([]lumePushPart, int64, error) {
	tmpPath, err := writeLumeDiskStreamTemp(diskPath)
	if err != nil {
		return nil, 0, err
	}
	defer os.Remove(tmpPath)

	tmp, err := os.Open(tmpPath)
	if err != nil {
		return nil, 0, fmt.Errorf("open temp stream: %w", err)
	}
	defer tmp.Close()

	info, err := tmp.Stat()
	if err != nil {
		return nil, 0, fmt.Errorf("stat temp stream: %w", err)
	}
	total := info.Size()
	if total == 0 {
		return nil, 0, fmt.Errorf("empty tar stream")
	}

	partTotal := int((total + chunkSize - 1) / chunkSize)
	parts := make([]lumePushPart, 0, partTotal)
	buf := make([]byte, 1<<20)
	for i := 0; i < partTotal; i++ {
		remaining := chunkSize
		if i == partTotal-1 {
			remaining = total - int64(i)*chunkSize
		}
		h := sha256.New()
		read := int64(0)
		for read < remaining {
			want := int64(len(buf))
			if remaining-read < want {
				want = remaining - read
			}
			n, err := io.ReadFull(tmp, buf[:want])
			if err != nil {
				return nil, 0, fmt.Errorf("read part %d: %w", i+1, err)
			}
			h.Write(buf[:n])
			read += int64(n)
		}
		number := i + 1
		parts = append(parts, lumePushPart{
			Number:    number,
			Title:     lumePartTitle(number),
			Size:      remaining,
			Digest:    "sha256:" + hex.EncodeToString(h.Sum(nil)),
			MediaType: fmt.Sprintf("%s;part.number=%d;part.total=%d", ociimage.LumeTarLayerMediaTypePrefix, number, partTotal),
		})
	}
	return parts, total, nil
}

func writeLumeDiskStreamTemp(diskPath string) (string, error) {
	tmp, err := os.CreateTemp("", "cove-lume-export-*.tar.gz")
	if err != nil {
		return "", fmt.Errorf("create temp stream: %w", err)
	}
	path := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			os.Remove(path)
		}
		if tmp != nil {
			tmp.Close()
		}
	}()
	if err := writeTarGzipStream(tmp, diskPath); err != nil {
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close temp stream: %w", err)
	}
	tmp = nil
	ok = true
	return path, nil
}

// writeTarGzipStream writes a gzip(tar(disk.img)) stream to w, where the
// single tar entry is the disk file with its on-disk size. Lume's reader
// concatenates tar parts and walks the resulting tar archive, picking out
// the regular file — name doesn't matter for compatibility, but we keep
// "disk.img" for clarity.
func writeTarGzipStream(w io.Writer, diskPath string) error {
	src, err := os.Open(diskPath)
	if err != nil {
		return fmt.Errorf("open disk: %w", err)
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return fmt.Errorf("stat disk: %w", err)
	}

	gz := gzip.NewWriter(w)
	tr := tar.NewWriter(gz)
	hdr := &tar.Header{
		Name:    "disk.img",
		Mode:    0644,
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}
	if err := tr.WriteHeader(hdr); err != nil {
		return fmt.Errorf("tar header: %w", err)
	}
	if _, err := io.Copy(tr, src); err != nil {
		return fmt.Errorf("tar body: %w", err)
	}
	if err := tr.Close(); err != nil {
		return fmt.Errorf("tar close: %w", err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("gzip close: %w", err)
	}
	return nil
}

// buildLumeManifest builds the OCI manifest pointing at the empty config
// blob, all disk parts in order, and the config.json + nvram sidecars.
//
// Lume's manifest carries an artifactType and uses the OCI empty config
// blob ({} base64-encoded). The disk part layers carry a parameterized
// tar mediaType; sidecars carry the OCI image config + octet-stream
// mediaTypes. All addressing is by org.opencontainers.image.title.
func buildLumeManifest(plan *lumePushPlan, configJSON []byte) ociimage.Manifest {
	emptyDigest := "sha256:" + hex.EncodeToString(sha256Sum([]byte("{}")))

	layers := make([]ociimage.Descriptor, 0, len(plan.Parts)+2)
	for _, p := range plan.Parts {
		layers = append(layers, ociimage.Descriptor{
			MediaType: p.MediaType,
			Size:      p.Size,
			Digest:    p.Digest,
			Annotations: map[string]string{
				"org.opencontainers.image.title": p.Title,
			},
		})
	}
	layers = append(layers, ociimage.Descriptor{
		MediaType: ociimage.MediaTypeImageConfig,
		Size:      int64(len(configJSON)),
		Digest:    "sha256:" + hex.EncodeToString(sha256Sum(configJSON)),
		Annotations: map[string]string{
			"org.opencontainers.image.title": ociimage.LumeConfigTitle,
		},
	})
	layers = append(layers, ociimage.Descriptor{
		MediaType: ociimage.MediaTypeLayer,
		Size:      plan.NvramSize,
		Digest:    plan.NvramDigest,
		Annotations: map[string]string{
			"org.opencontainers.image.title": ociimage.LumeNvramTitle,
		},
	})

	return ociimage.Manifest{
		SchemaVersion: 2,
		MediaType:     ociimage.MediaTypeImageManifest,
		Config: ociimage.Descriptor{
			MediaType: "application/vnd.oci.empty.v1+json",
			Size:      2,
			Digest:    emptyDigest,
		},
		Layers: layers,
		Annotations: map[string]string{
			"org.opencontainers.image.created": plan.UploadTime,
		},
	}
}

func sha256Sum(b []byte) []byte {
	h := sha256.New()
	h.Write(b)
	return h.Sum(nil)
}

// printLumePushDryRun mirrors printPushDryRun's shape but prints lume-format
// metadata: tar-split parts (count, sizes), sidecars (config.json, nvram.bin),
// and the projected lume config fields.
func printLumePushDryRun(w io.Writer, plan *lumePushPlan) {
	fmt.Fprintln(w, "Push dry run (lume tar-split format)")
	fmt.Fprintf(w, "  vm: %s\n", plan.VMName)
	fmt.Fprintf(w, "  ref: %s\n", plan.Ref)
	fmt.Fprintf(w, "  disk: %s (%s)\n", plan.DiskPath, bytefmt.Size(plan.DiskSize))
	fmt.Fprintf(w, "  part size: %s\n", bytefmt.Size(plan.ChunkSize))
	fmt.Fprintf(w, "  parts: %d (total compressed %s)\n", len(plan.Parts), bytefmt.Size(plan.StreamSize))
	if len(plan.Parts) > 0 {
		first := plan.Parts[0]
		last := plan.Parts[len(plan.Parts)-1]
		fmt.Fprintf(w, "  first part: %s (%s)\n", first.Title, bytefmt.Size(first.Size))
		fmt.Fprintf(w, "  last part:  %s (%s)\n", last.Title, bytefmt.Size(last.Size))
	}
	fmt.Fprintf(w, "  config.json: %d B\n", len(plan.ConfigJSON))
	fmt.Fprintf(w, "  nvram.bin:   %s\n", bytefmt.Size(plan.NvramSize))
	fmt.Fprintln(w, "  projected lume config:")
	fmt.Fprintf(w, "    os:         %s\n", plan.Config.OS)
	fmt.Fprintf(w, "    cpuCount:   %d\n", plan.Config.CPUCount)
	fmt.Fprintf(w, "    memorySize: %s\n", bytefmt.Size(int64(plan.Config.MemorySize)))
	fmt.Fprintf(w, "    diskSize:   %s\n", bytefmt.Size(int64(plan.Config.DiskSize)))
	if plan.Config.Display != "" {
		fmt.Fprintf(w, "    display:    %s\n", plan.Config.Display)
	}
	if plan.Config.MACAddress != "" {
		fmt.Fprintf(w, "    macAddress: %s\n", plan.Config.MACAddress)
	}
}

func printLumePushResult(w io.Writer, plan *lumePushPlan, opts pushOptions) {
	fmt.Fprintln(w, "Push complete (lume tar-split format)")
	fmt.Fprintf(w, "  vm: %s\n", plan.VMName)
	fmt.Fprintf(w, "  ref: %s\n", plan.Ref)
	fmt.Fprintf(w, "  parts: %d (total compressed %s)\n", len(plan.Parts), bytefmt.Size(plan.StreamSize))
	if len(opts.AdditionalTags) > 0 {
		fmt.Fprintf(w, "  additional tags: %s\n", strings.Join(opts.AdditionalTags, ", "))
	}
}

// writeLumeManifestOut serializes plan.Manifest as JSON to path.
func writeLumeManifestOut(path string, m ociimage.Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal lume manifest: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write lume manifest: %w", err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		return fmt.Errorf("chmod lume manifest: %w", err)
	}
	return nil
}

func runLumePush(ctx context.Context, plan *lumePushPlan, opts pushOptions) error {
	if !opts.DryRun {
		if err := pushLumeImage(ctx, plan, opts); err != nil {
			return err
		}
		if opts.ManifestOut != "" {
			if err := writeLumeManifestOut(opts.ManifestOut, plan.Manifest); err != nil {
				return err
			}
		}
		printLumePushResult(os.Stdout, plan, opts)
		return nil
	}
	if opts.ManifestOut != "" {
		if err := writeLumeManifestOut(opts.ManifestOut, plan.Manifest); err != nil {
			return err
		}
	}
	printLumePushDryRun(os.Stdout, plan)
	return nil
}

func pushLumeImage(ctx context.Context, plan *lumePushPlan, opts pushOptions) error {
	ref, err := ociimage.ParseReference(plan.Ref)
	if err != nil {
		return fmt.Errorf("cove push: invalid target ref %q: %w", plan.Ref, err)
	}
	client := pushRegistryClient(ref, opts)
	if err := uploadBytesBlob(ctx, client, ref, plan.Manifest.Config, []byte("{}")); err != nil {
		return err
	}

	streamPath, err := writeLumeDiskStreamTemp(plan.DiskPath)
	if err != nil {
		return err
	}
	defer os.Remove(streamPath)

	var offset int64
	for _, part := range plan.Parts {
		desc := ociimage.Descriptor{MediaType: part.MediaType, Size: part.Size, Digest: part.Digest}
		if err := uploadFileSectionBlob(ctx, client, ref, desc, streamPath, offset); err != nil {
			return fmt.Errorf("upload %s: %w", part.Title, err)
		}
		offset += part.Size
	}

	configDesc, err := lumeLayerDescriptor(plan.Manifest, ociimage.LumeConfigTitle)
	if err != nil {
		return err
	}
	if err := uploadBytesBlob(ctx, client, ref, configDesc, plan.ConfigJSON); err != nil {
		return err
	}
	nvramDesc, err := lumeLayerDescriptor(plan.Manifest, ociimage.LumeNvramTitle)
	if err != nil {
		return err
	}
	if err := uploadFileBlob(ctx, client, ref, nvramDesc, plan.NvramPath); err != nil {
		return err
	}

	if _, err := client.PushManifest(ctx, ref, plan.Manifest); err != nil {
		return err
	}
	for _, tag := range opts.AdditionalTags {
		extra := ref
		extra.Tag = tag
		if _, err := client.PushManifest(ctx, extra, plan.Manifest); err != nil {
			return err
		}
	}
	return nil
}

func lumeLayerDescriptor(m ociimage.Manifest, title string) (ociimage.Descriptor, error) {
	for _, layer := range m.Layers {
		if layer.Annotations["org.opencontainers.image.title"] == title {
			return layer, nil
		}
	}
	return ociimage.Descriptor{}, fmt.Errorf("lume manifest missing %s layer", title)
}

func uploadFileSectionBlob(ctx context.Context, client ociimage.RegistryClient, ref ociimage.Reference, desc ociimage.Descriptor, path string, offset int64) error {
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
	if err := verifyFileSectionDigest(f, desc, offset); err != nil {
		return err
	}
	return client.UploadBlob(ctx, ref, desc, io.NewSectionReader(f, offset, desc.Size))
}

func verifyFileSectionDigest(f *os.File, desc ociimage.Descriptor, offset int64) error {
	if offset < 0 {
		return fmt.Errorf("negative blob offset %d", offset)
	}
	if desc.Size < 0 {
		return fmt.Errorf("negative blob size %d", desc.Size)
	}
	h := sha256.New()
	n, err := io.Copy(h, io.NewSectionReader(f, offset, desc.Size))
	if err != nil {
		return fmt.Errorf("hash blob section: %w", err)
	}
	if n != desc.Size {
		return fmt.Errorf("hash blob section: read %d bytes, want %d", n, desc.Size)
	}
	got := "sha256:" + hex.EncodeToString(h.Sum(nil))
	if got != desc.Digest {
		return fmt.Errorf("blob digest %s, want %s", got, desc.Digest)
	}
	return nil
}
