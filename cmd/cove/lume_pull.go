// lume_pull.go - Import VMs from lume's tar-split ghcr.io images.
//
// Lume publishes VM disks as N tar parts addressed by part.aa..part.bo (or
// equivalent part.number= mediaType parameter). The parts byte-concatenate
// into a single tar stream that contains a single regular file (the disk
// image). Sidecars `nvram.bin` and `config.json` ship as their own layers
// keyed by org.opencontainers.image.title.
//
// This file is the import entry-point. It downloads each part, decompresses
// the gzip wrapper if present, and concatenates the tar streams onto a
// streaming reader, then extracts the single disk file out of the combined
// tar. nvram.bin and config.json are dropped into the VM directory verbatim.

package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/tmc/cove/internal/ociimage"
	"github.com/tmc/cove/internal/vmconfig"
)

// lumePullDisk downloads, concatenates, and untars a lume image into the
// destination VM directory.
func lumePullDisk(ctx context.Context, plan *pullPlan, opts pullOptions) error {
	if plan == nil {
		return fmt.Errorf("cove pull: missing pull plan")
	}
	if len(plan.Manifest.Lume.DiskParts) == 0 {
		return fmt.Errorf("cove pull: lume manifest has no disk parts")
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

	// Sidecars first — they're cheap and let us fail fast on auth issues.
	// Lume's config.json is preserved verbatim as lume-config.json so we
	// don't overwrite cove's own config.json (which is the VM's hardware
	// settings file). The fields from lume's config that map onto cove's
	// schema are extracted into cove's config.json below.
	if err := lumePullSidecar(ctx, client, plan, plan.Manifest.Lume.NvramLayer, "nvram.bin"); err != nil {
		return err
	}
	if err := lumePullSidecar(ctx, client, plan, plan.Manifest.Lume.ConfigLayer, "lume-config.json"); err != nil {
		return err
	}
	if err := lumeWriteCoveConfig(plan); err != nil {
		return err
	}

	// Stream the concatenated tar parts into a single tar reader.
	partialPath := filepath.Join(plan.VMDir, "disk.img.partial")
	diskPath := filepath.Join(plan.VMDir, "disk.img")
	if err := lumeStreamDisk(ctx, client, plan, partialPath); err != nil {
		os.Remove(partialPath)
		return err
	}
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

// lumePullSidecar fetches a non-disk layer and writes it under VMDir/name.
// If desc is nil the call is a no-op (the sidecar isn't present in the manifest).
func lumePullSidecar(ctx context.Context, client ociimage.RegistryClient, plan *pullPlan, desc *ociimage.Descriptor, name string) error {
	if desc == nil {
		return nil
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
	if desc.Digest != "" {
		got := "sha256:" + hex.EncodeToString(h.Sum(nil))
		if got != desc.Digest {
			f.Close()
			os.Remove(tmp)
			return fmt.Errorf("write %s: digest %s, want %s", name, got, desc.Digest)
		}
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

// lumeStreamDisk concatenates each tar part into a single tar reader and
// extracts the single disk file into partialPath. Lume's tar parts are gzip-
// wrapped; we sniff for the gzip magic on the first chunk and wrap accordingly.
func lumeStreamDisk(ctx context.Context, client ociimage.RegistryClient, plan *pullPlan, partialPath string) error {
	pr, pw := io.Pipe()

	go func() {
		err := lumeFeedTarStream(ctx, client, plan, pw)
		pw.CloseWithError(err)
	}()
	defer pr.Close()

	// Lume wraps each tar part in gzip. We read the combined byte stream
	// through a single gzip.Reader; concatenated gzip members are valid
	// gzip per RFC 1952, so a single Reader handles the whole sequence
	// when MultiStream is left at its default (true).
	gz, err := gzip.NewReader(pr)
	if err != nil {
		return fmt.Errorf("read lume gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	out, err := os.OpenFile(partialPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("open partial disk: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			out.Close()
		}
	}()

	wroteDisk := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read lume tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		// Lume's disk part archives carry exactly one regular entry; its
		// name varies (often "disk.img" but lume has shipped variants).
		// Prefer the largest regular file we see.
		if wroteDisk {
			// Skip additional regular entries by reading them away.
			if _, err := io.Copy(io.Discard, tr); err != nil {
				return fmt.Errorf("skip lume tar entry: %w", err)
			}
			continue
		}
		if _, err := io.Copy(out, tr); err != nil {
			return fmt.Errorf("write disk: %w", err)
		}
		wroteDisk = true
	}
	if !wroteDisk {
		return fmt.Errorf("lume tar contains no regular file")
	}
	if err := out.Sync(); err != nil {
		return fmt.Errorf("sync partial disk: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close partial disk: %w", err)
	}
	closed = true
	return nil
}

// lumeWriteCoveConfig reads the lume sidecar config that lumePullSidecar
// dropped into VMDir/lume-config.json and projects the fields that map onto
// cove's vmconfig.Config (CPU, memory). Lume-only fields (machineIdentifier,
// hardwareModel, MAC, disk size, OS) stay in lume-config.json untouched —
// cove's runtime reads those from disk on first boot, not from config.json.
//
// Missing or unparseable fields are skipped rather than treated as errors:
// a partial map is better than no map, and cove falls back to defaults for
// unset fields.
func lumeWriteCoveConfig(plan *pullPlan) error {
	path := filepath.Join(plan.VMDir, "lume-config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read lume-config.json: %w", err)
	}
	lc, err := ociimage.DecodeLumeConfig(data)
	if err != nil {
		return err
	}
	cfg, err := vmconfig.Load(plan.VMDir)
	if err != nil {
		return err
	}
	if cfg == nil {
		cfg = &vmconfig.Config{}
	}
	if lc.CPU > 0 {
		cfg.CPU = uint(lc.CPU)
	}
	if mem, ok := parseLumeMemory(lc.Memory); ok {
		cfg.MemoryGB = mem
	}
	return vmconfig.Save(plan.VMDir, cfg)
}

// parseLumeMemory reads lume's memory string ("4G", "4GB", "4096M", "4096MB",
// or a bare byte count) and returns the value in whole gigabytes. The returned
// bool reports whether the input was understood; an empty string or unknown
// suffix yields false so callers can leave the destination field at its zero
// value.
//
// Cove's vmconfig.Config.MemoryGB is uint64 gigabytes. Sub-GB lume sizes round
// down to zero and are reported as not-ok so the caller doesn't silently
// downgrade a VM to 0 GB.
func parseLumeMemory(s string) (uint64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	// Split numeric prefix from suffix.
	end := 0
	for end < len(s) && (s[end] >= '0' && s[end] <= '9') {
		end++
	}
	if end == 0 {
		return 0, false
	}
	num, err := strconv.ParseUint(s[:end], 10, 64)
	if err != nil {
		return 0, false
	}
	suffix := strings.ToUpper(strings.TrimSpace(s[end:]))
	var bytes uint64
	switch suffix {
	case "", "B":
		bytes = num
	case "K", "KB":
		bytes = num * 1024
	case "M", "MB":
		bytes = num * 1024 * 1024
	case "G", "GB":
		bytes = num * 1024 * 1024 * 1024
	case "T", "TB":
		bytes = num * 1024 * 1024 * 1024 * 1024
	default:
		return 0, false
	}
	gb := bytes / (1024 * 1024 * 1024)
	if gb == 0 {
		return 0, false
	}
	return gb, true
}

type lumeFetchedPart struct {
	index int
	part  ociimage.LumeLayer
	path  string
	err   error
}

// lumeFeedTarStream fetches disk parts concurrently and writes them in
// part-number order into w.
func lumeFeedTarStream(ctx context.Context, client ociimage.RegistryClient, plan *pullPlan, w io.Writer) error {
	parts := plan.Manifest.Lume.DiskParts
	if len(parts) == 0 {
		return nil
	}
	cacheDir, err := os.MkdirTemp(plan.VMDir, ".lume-parts-")
	if err != nil {
		return fmt.Errorf("create lume part cache: %w", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan int, len(parts))
	for i := range parts {
		jobs <- i
	}
	close(jobs)

	workers := pullChunkWorkers
	if len(parts) < workers {
		workers = len(parts)
	}
	results := make(chan lumeFetchedPart, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				if err := ctx.Err(); err != nil {
					return
				}
				result := lumeFetchPart(ctx, client, plan.Ref, parts[index], cacheDir, index)
				select {
				case results <- result:
				case <-ctx.Done():
					if result.path != "" {
						_ = os.Remove(result.path)
					}
					return
				}
			}
		}()
	}
	defer func() {
		cancel()
		wg.Wait()
		_ = os.RemoveAll(cacheDir)
	}()

	pending := make(map[int]lumeFetchedPart)
	next := 0
	for received := 0; received < len(parts); received++ {
		var result lumeFetchedPart
		select {
		case result = <-results:
		case <-ctx.Done():
			return ctx.Err()
		}
		if result.err != nil {
			return result.err
		}
		pending[result.index] = result
		for {
			ready, ok := pending[next]
			if !ok {
				break
			}
			if err := lumeWriteFetchedPart(w, ready); err != nil {
				return err
			}
			delete(pending, next)
			next++
		}
	}
	return nil
}

func lumeFetchPart(ctx context.Context, client ociimage.RegistryClient, ref ociimage.Reference, part ociimage.LumeLayer, dir string, index int) lumeFetchedPart {
	result := lumeFetchedPart{index: index, part: part}
	label := lumePartLabel(part, index)
	body, err := client.FetchBlob(ctx, ref, part.Descriptor.Digest)
	if err != nil {
		result.err = fmt.Errorf("fetch part %s: %w", label, err)
		return result
	}

	path := filepath.Join(dir, fmt.Sprintf("part-%06d", index))
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		_ = body.Close()
		result.err = fmt.Errorf("open part %s: %w", label, err)
		return result
	}
	h := sha256.New()
	n, copyErr := io.Copy(f, io.TeeReader(body, h))
	bodyErr := body.Close()
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(path)
		result.err = fmt.Errorf("read part %s: %w", label, copyErr)
		return result
	}
	if bodyErr != nil {
		_ = os.Remove(path)
		result.err = fmt.Errorf("close part %s: %w", label, bodyErr)
		return result
	}
	if closeErr != nil {
		_ = os.Remove(path)
		result.err = fmt.Errorf("close part %s: %w", label, closeErr)
		return result
	}
	if part.Descriptor.Size > 0 && n != part.Descriptor.Size {
		_ = os.Remove(path)
		result.err = fmt.Errorf("read part %s: size %d, want %d", label, n, part.Descriptor.Size)
		return result
	}
	if part.Descriptor.Digest != "" {
		got := "sha256:" + hex.EncodeToString(h.Sum(nil))
		if got != part.Descriptor.Digest {
			_ = os.Remove(path)
			result.err = fmt.Errorf("read part %s: digest %s, want %s", label, got, part.Descriptor.Digest)
			return result
		}
	}
	result.path = path
	return result
}

func lumeWriteFetchedPart(w io.Writer, part lumeFetchedPart) error {
	label := lumePartLabel(part.part, part.index)
	f, err := os.Open(part.path)
	if err != nil {
		return fmt.Errorf("open part %s: %w", label, err)
	}
	_, copyErr := io.Copy(w, f)
	closeErr := f.Close()
	if removeErr := os.Remove(part.path); removeErr != nil && copyErr == nil && closeErr == nil {
		return fmt.Errorf("remove part %s: %w", label, removeErr)
	}
	if copyErr != nil {
		return fmt.Errorf("write part %s: %w", label, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close part %s: %w", label, closeErr)
	}
	return nil
}

func lumePartLabel(part ociimage.LumeLayer, index int) string {
	if part.Title != "" {
		return part.Title
	}
	if part.PartNumber > 0 {
		return fmt.Sprintf("%d", part.PartNumber)
	}
	return fmt.Sprintf("%d", index+1)
}
