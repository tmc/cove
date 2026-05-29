// tart_pull.go — Import VMs from cirruslabs/tart's ghcr.io images.
//
// Tart packages a macOS VM as one config layer
// (application/vnd.cirruslabs.tart.config.v1), N disk chunks at 512 MiB
// each (application/vnd.cirruslabs.tart.disk.v2) compressed with Apple's
// framed LZ4, and one nvram blob (application/vnd.cirruslabs.tart.nvram.v1).
//
// This file is the import entry-point. It downloads each disk chunk,
// decompresses the Apple-LZ4 stream into uncompressed bytes, verifies the
// per-layer org.cirruslabs.tart.uncompressed-content-digest annotation,
// and writes the result at the layer's cumulative offset on a pre-sized
// sparse disk image. The nvram blob is written as aux.img (cove's name
// for the per-VM EFI variables file). The tart config layer is preserved
// verbatim as tart-config.json and projected onto cove's vmconfig.Config
// for the fields that map (CPU, memory).
//
// See docs/research/cove-tart-compat.md for the full format reference.

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/tmc/cove/internal/ociimage"
	"github.com/tmc/cove/internal/vmconfig"
)

// tartPullDisk fetches the tart manifest's nvram, config, and disk layers
// and stages them into the destination VM directory in cove's layout.
func tartPullDisk(ctx context.Context, plan *pullPlan, opts pullOptions) error {
	if plan == nil {
		return fmt.Errorf("cove pull: missing pull plan")
	}
	if len(plan.Manifest.Tart.DiskLayers) == 0 {
		return fmt.Errorf("cove pull: tart manifest has no disk layers")
	}
	if err := os.MkdirAll(plan.VMDir, 0755); err != nil {
		return fmt.Errorf("create VM directory: %w", err)
	}
	if plan.VMName != "" {
		if err := vmconfig.EnsureCompatibilityAlias(plan.VMName, plan.VMDir); err != nil {
			return fmt.Errorf("create VM compatibility alias: %w", err)
		}
	}

	client := pullRegistryClient(plan.Ref, opts)

	// Sidecars first — they're cheap, fail fast on auth/network issues, and
	// let us read the tart config to project memory/CPU before the slow
	// disk decode. nvram → aux.img mirrors cove's on-disk layout for EFI
	// variables (cove pull's metadata path also lands nvram at aux.img).
	if err := tartPullSidecar(ctx, client, plan, plan.Manifest.Tart.NVRAMLayer, "aux.img"); err != nil {
		return err
	}
	if err := tartPullSidecar(ctx, client, plan, plan.Manifest.Tart.ConfigLayer, "tart-config.json"); err != nil {
		return err
	}
	if err := tartWriteCoveConfig(plan); err != nil {
		return err
	}

	partialPath := filepath.Join(plan.VMDir, "disk.img.partial")
	diskPath := filepath.Join(plan.VMDir, "disk.img")
	disk, err := ociimage.CreatePartialDisk(partialPath, plan.Manifest.Tart.UncompressedDiskSize)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			disk.Close()
		}
	}()

	if err := tartPullDiskLayers(ctx, client, plan, disk); err != nil {
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

// tartPullSidecar fetches a single non-disk layer and writes the body
// verbatim at VMDir/name. The body is compared against desc.Size and
// desc.Digest before the rename — a corrupt blob never lands at its
// final path.
func tartPullSidecar(ctx context.Context, client ociimage.RegistryClient, plan *pullPlan, desc ociimage.Descriptor, name string) error {
	if desc.Digest == "" {
		return fmt.Errorf("tart pull: sidecar %s missing digest", name)
	}
	body, err := client.FetchBlob(ctx, plan.Ref, desc.Digest)
	if err != nil {
		return fmt.Errorf("fetch %s: %w", name, err)
	}
	defer body.Close()

	dst := filepath.Join(plan.VMDir, name)
	tmp := dst + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("open %s: %w", name, err)
	}
	h := sha256.New()
	n, copyErr := io.Copy(f, io.TeeReader(body, h))
	if copyErr != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write %s: %w", name, copyErr)
	}
	if desc.Size > 0 && n != desc.Size {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write %s: size %d, want %d", name, n, desc.Size)
	}
	got := "sha256:" + hex.EncodeToString(h.Sum(nil))
	if got != desc.Digest {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write %s: digest %s, want %s", name, got, desc.Digest)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync %s: %w", name, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", name, err)
	}
	return os.Rename(tmp, dst)
}

// tartPullDiskLayers fetches each disk-v2 layer, decompresses Apple-LZ4,
// verifies the uncompressed content digest, and writes at the cumulative
// offset on disk. Up to pullChunkWorkers layers run concurrently — the
// pre-sized sparse file makes WriteAt at non-overlapping offsets safe.
func tartPullDiskLayers(ctx context.Context, client ociimage.RegistryClient, plan *pullPlan, disk io.WriterAt) error {
	layers := plan.Manifest.Tart.DiskLayers
	if len(layers) == 0 {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan ociimage.TartDiskLayer)
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
				if err := tartPullDiskLayer(ctx, client, plan.Ref, disk, layer); err != nil {
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

// tartPullDiskLayer fetches one disk-v2 blob, decodes the Apple-LZ4 stream,
// verifies the uncompressed bytes against the per-layer content digest,
// and writes at the layer's cumulative offset.
//
// The whole compressed and uncompressed payloads are buffered in memory:
// tart layers cap at 512 MiB compressed and 512 MiB uncompressed, well
// within RAM on Apple Silicon hosts. A streaming decoder would let us
// trade RAM for code complexity; cove's pullChunkWorkers cap (4) bounds
// peak usage to ~4 GiB which is acceptable.
func tartPullDiskLayer(ctx context.Context, client ociimage.RegistryClient, ref ociimage.Reference, disk io.WriterAt, layer ociimage.TartDiskLayer) error {
	body, err := client.FetchBlob(ctx, ref, layer.Descriptor.Digest)
	if err != nil {
		return fmt.Errorf("fetch tart disk layer %s: %w", layer.Descriptor.Digest, err)
	}
	compressed, copyErr := io.ReadAll(body)
	closeErr := body.Close()
	if copyErr != nil {
		return fmt.Errorf("read tart disk layer %s: %w", layer.Descriptor.Digest, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close tart disk layer %s: %w", layer.Descriptor.Digest, closeErr)
	}

	uncompressed, err := ociimage.DecompressAppleLZ4(compressed)
	if err != nil {
		return fmt.Errorf("decompress tart disk layer %s: %w", layer.Descriptor.Digest, err)
	}
	if int64(len(uncompressed)) != layer.UncompressedSize {
		return fmt.Errorf("tart disk layer %s: decompressed %d bytes, want %d", layer.Descriptor.Digest, len(uncompressed), layer.UncompressedSize)
	}
	sum := sha256.Sum256(uncompressed)
	got := "sha256:" + hex.EncodeToString(sum[:])
	if got != layer.UncompressedContentDigest {
		return fmt.Errorf("tart disk layer %s: content digest %s, want %s", layer.Descriptor.Digest, got, layer.UncompressedContentDigest)
	}
	if _, err := disk.WriteAt(uncompressed, layer.Offset); err != nil {
		return fmt.Errorf("write tart disk layer %s: %w", layer.Descriptor.Digest, err)
	}
	return nil
}

// tartConfig is the subset of tart's VMConfig.swift that cove projects onto
// vmconfig.Config. Fields cove can't model (display, MAC, OS, arch,
// platform.*) are ignored — the full document still ships in tart-config.json
// for round-trip fidelity.
type tartConfig struct {
	CPUCount   uint   `json:"cpuCount,omitempty"`
	MemorySize uint64 `json:"memorySize,omitempty"`
}

// tartWriteCoveConfig reads the tart-config.json sidecar that
// tartPullSidecar wrote and projects CPU/memory onto cove's vmconfig.Config.
//
// Memory comes in as bytes in tart's config; cove's vmconfig.Config.MemoryGB
// is whole gigabytes. Sub-GB sizes round down to zero and the field stays
// at its zero value — caller falls back to defaults rather than booting a
// 0 GB VM. CPU is direct.
//
// Missing or unparseable fields are skipped, not treated as errors: a
// partial map is better than no map.
func tartWriteCoveConfig(plan *pullPlan) error {
	path := filepath.Join(plan.VMDir, "tart-config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read tart-config.json: %w", err)
	}
	var tc tartConfig
	if err := json.Unmarshal(data, &tc); err != nil {
		// Tolerate a malformed config — preserve the raw blob and move on.
		return nil
	}
	cfg, err := vmconfig.Load(plan.VMDir)
	if err != nil {
		return err
	}
	if cfg == nil {
		cfg = &vmconfig.Config{}
	}
	if tc.CPUCount > 0 {
		cfg.CPU = tc.CPUCount
	}
	if gb := tc.MemorySize / (1024 * 1024 * 1024); gb > 0 {
		cfg.MemoryGB = gb
	}
	return vmconfig.Save(plan.VMDir, cfg)
}
