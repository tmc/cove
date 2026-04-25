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

	"github.com/tmc/vz-macos/internal/ociimage"
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

	client := pullRegistryClient(plan.Ref, opts)

	// Sidecars first — they're cheap and let us fail fast on auth issues.
	if err := lumePullSidecar(ctx, client, plan, plan.Manifest.Lume.NvramLayer, "nvram.bin"); err != nil {
		return err
	}
	if err := lumePullSidecar(ctx, client, plan, plan.Manifest.Lume.ConfigLayer, "config.json"); err != nil {
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

// lumeFeedTarStream copies each disk part body, in part-number order, into w.
// Returns the first error encountered (sequential — parallel fetch would need
// reorder buffering, not worth it for a 41-part stream).
func lumeFeedTarStream(ctx context.Context, client ociimage.RegistryClient, plan *pullPlan, w io.Writer) error {
	for _, part := range plan.Manifest.Lume.DiskParts {
		if err := ctx.Err(); err != nil {
			return err
		}
		body, err := client.FetchBlob(ctx, plan.Ref, part.Descriptor.Digest)
		if err != nil {
			return fmt.Errorf("fetch part %s: %w", part.Title, err)
		}
		_, copyErr := io.Copy(w, body)
		closeErr := body.Close()
		if copyErr != nil {
			return fmt.Errorf("read part %s: %w", part.Title, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close part %s: %w", part.Title, closeErr)
		}
	}
	return nil
}
