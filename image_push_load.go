// image_push_load.go — local-tarball portability for the image store.
//
// `cove image push <ref> <file>` tars a built image directory into a single
// file (suitable for scp / external storage). `cove image load <file>`
// extracts it back into ~/.vz/images/<ref>/. Pass `-` for either side to
// stream via stdin/stdout (e.g. `cove image push x:1 - | ssh host cove
// image load -`). No HTTP, no OCI registry.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"
)

// imageDataFiles is the deterministic write order for the four sidecar
// files an image directory must contain. The push side fails loud if any
// is missing; the load side rejects a tar that lacks one.
var imageDataFiles = []string{"disk.img", "aux.img", "hw.model", "machine.id"}

// imageEntryMaxBytes caps a single tar entry. Cove disk images can be
// many tens of GB, but >100 GB is treated as malicious / corrupt input.
const imageEntryMaxBytes int64 = 100 * 1024 * 1024 * 1024

func runImagePush(args []string) error {
	fs := flag.NewFlagSet("image push", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	gz := fs.Bool("gzip", false, "gzip-compress the tarball")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		fs.Usage()
		return fmt.Errorf("image push requires <ref> and <file>")
	}
	ref, err := ParseImageRef(fs.Arg(0))
	if err != nil {
		return err
	}
	dst := fs.Arg(1)
	if isRegistryReference(dst) {
		if *gz {
			return fmt.Errorf("image push: -gzip is only valid for tarball export")
		}
		desc, err := PushImageToRegistry(context.Background(), ref, dst)
		if err != nil {
			return err
		}
		fmt.Printf("Pushed image %s to %s\n", ref, dst)
		fmt.Printf("  digest: %s\n", desc.Digest)
		return nil
	}
	if dst == "-" {
		if term.IsTerminal(int(os.Stdout.Fd())) {
			return fmt.Errorf("image push: refusing to write tarball to a TTY (redirect stdout or pass a file path)")
		}
		if err := WriteImageTar(ref, os.Stdout, *gz); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Pushed image %s to stdout\n", ref)
		return nil
	}
	if err := PushImageToFile(ref, dst, *gz); err != nil {
		return err
	}
	fmt.Printf("Pushed image %s to %s\n", ref, dst)
	return nil
}

// PushImageToFile tars an image directory to dst. Writes to dst+".tmp"
// and renames on success; cleans the temp file on any error.
func PushImageToFile(ref ImageRef, dst string, gzipOut bool) error {
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("image push: open %s: %w", tmp, err)
	}
	cleanup := func() { os.Remove(tmp) }
	closed := false
	defer func() {
		if !closed {
			out.Close()
			cleanup()
		}
	}()
	if err := WriteImageTar(ref, out, gzipOut); err != nil {
		return err
	}
	if err := out.Sync(); err != nil {
		return fmt.Errorf("image push: sync: %w", err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("image push: close: %w", err)
	}
	closed = true
	if err := os.Rename(tmp, dst); err != nil {
		cleanup()
		return fmt.Errorf("image push: rename: %w", err)
	}
	return nil
}

// WriteImageTar streams an image directory tarball to w. Used by both
// the file path (PushImageToFile) and the stdout (`cove image push <ref> -`)
// code paths. The same strict deterministic write order is preserved.
func WriteImageTar(ref ImageRef, w io.Writer, gzipOut bool) error {
	if !ImageExists(ref) {
		return fmt.Errorf("image push: %s not found in store", ref)
	}
	imgDir := ref.Path()
	manifestPath := filepath.Join(imgDir, "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		return fmt.Errorf("image push: manifest missing: %w", err)
	}
	for _, name := range imageDataFiles {
		if _, err := os.Stat(filepath.Join(imgDir, name)); err != nil {
			return fmt.Errorf("image push: source missing %s: %w", name, err)
		}
	}

	var tw *tar.Writer
	var gz *gzip.Writer
	if gzipOut {
		gz = gzip.NewWriter(w)
		tw = tar.NewWriter(gz)
	} else {
		tw = tar.NewWriter(w)
	}

	if err := writeFileToTar(tw, imgDir, "manifest.json"); err != nil {
		return fmt.Errorf("image push: %w", err)
	}
	for _, name := range imageDataFiles {
		if err := writeFileToTar(tw, imgDir, name); err != nil {
			return fmt.Errorf("image push: %w", err)
		}
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("image push: close tar: %w", err)
	}
	if gz != nil {
		if err := gz.Close(); err != nil {
			return fmt.Errorf("image push: close gzip: %w", err)
		}
	}
	return nil
}

// writeFileToTar copies srcDir/name into tw with explicit header fields.
// Mirrors lume_push.go:284-316 — Mode/Size/ModTime set from os.FileInfo;
// no Uname/Gname (those leak host identity into a portable tarball).
func writeFileToTar(tw *tar.Writer, srcDir, name string) error {
	src, err := os.Open(filepath.Join(srcDir, name))
	if err != nil {
		return fmt.Errorf("open %s: %w", name, err)
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", name, err)
	}
	hdr := &tar.Header{
		Name:    name,
		Mode:    0o644,
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("tar header %s: %w", name, err)
	}
	if _, err := io.Copy(tw, src); err != nil {
		return fmt.Errorf("tar body %s: %w", name, err)
	}
	return nil
}

func runImageLoad(args []string) error {
	fs := flag.NewFlagSet("image load", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	tag := fs.String("tag", "", "override image ref on load (name[:tag])")
	force := fs.Bool("force", false, "overwrite if image already exists")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return fmt.Errorf("image load requires <file>")
	}
	src := fs.Arg(0)
	if src == "-" {
		if term.IsTerminal(int(os.Stdin.Fd())) {
			return fmt.Errorf("image load: refusing to read tarball from a TTY (redirect stdin or pass a file path)")
		}
		ref, err := ReadImageTar(os.Stdin, *tag, *force)
		if err != nil {
			return err
		}
		fmt.Printf("Loaded image %s\n", ref)
		return nil
	}
	ref, err := LoadImageFromFile(src, *tag, *force)
	if err != nil {
		return err
	}
	fmt.Printf("Loaded image %s\n", ref)
	return nil
}

func runImagePull(args []string) error {
	fs := flag.NewFlagSet("image pull", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	tag := fs.String("tag", "", "override image ref on pull (name[:tag])")
	force := fs.Bool("force", false, "overwrite if image already exists")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return fmt.Errorf("image pull requires <registry/ref:tag>")
	}
	ref, desc, err := PullImageFromRegistry(context.Background(), fs.Arg(0), *tag, *force)
	if err != nil {
		return err
	}
	fmt.Printf("Pulled image %s from %s\n", ref, fs.Arg(0))
	fmt.Printf("  digest: %s\n", desc.Digest)
	return nil
}

// LoadImageFromFile extracts a tarball into the local image store and
// returns the ref under which the image was registered. The first tar
// entry must be manifest.json; the embedded name+tag (or overrideTag)
// is re-validated via ParseImageRef before any path is constructed.
func LoadImageFromFile(src, overrideTag string, force bool) (ImageRef, error) {
	f, err := os.Open(src)
	if err != nil {
		return ImageRef{}, fmt.Errorf("image load: open %s: %w", src, err)
	}
	defer f.Close()

	var r io.Reader = f
	if strings.HasSuffix(strings.ToLower(src), ".gz") || strings.HasSuffix(strings.ToLower(src), ".tgz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return ImageRef{}, fmt.Errorf("image load: gzip: %w", err)
		}
		defer gz.Close()
		r = gz
	}
	return readImageTarStream(r, overrideTag, force)
}

// ReadImageTar extracts an image tarball from r into the local image store.
// Mirrors LoadImageFromFile's auto-gzip detection by sniffing the first two
// bytes for the gzip magic before handing off to the tar parser.
func ReadImageTar(r io.Reader, overrideTag string, force bool) (ImageRef, error) {
	br, err := maybeGunzip(r)
	if err != nil {
		return ImageRef{}, err
	}
	return readImageTarStream(br, overrideTag, force)
}

// maybeGunzip peeks the first two bytes; if they match the gzip magic,
// wraps r in a gzip.Reader. The peeked bytes are spliced back via
// io.MultiReader so the downstream parser sees the full stream regardless
// of branch.
func maybeGunzip(r io.Reader) (io.Reader, error) {
	buf := make([]byte, 2)
	n, err := io.ReadFull(r, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, fmt.Errorf("image load: peek: %w", err)
	}
	combined := io.MultiReader(bytes.NewReader(buf[:n]), r)
	if n == 2 && buf[0] == 0x1f && buf[1] == 0x8b {
		gz, err := gzip.NewReader(combined)
		if err != nil {
			return nil, fmt.Errorf("image load: gzip: %w", err)
		}
		return gz, nil
	}
	return combined, nil
}

// readImageTarStream is the shared core for LoadImageFromFile / ReadImageTar.
// It assumes any gzip framing has already been peeled.
func readImageTarStream(r io.Reader, overrideTag string, force bool) (ImageRef, error) {
	tr := tar.NewReader(r)

	hdr, err := tr.Next()
	if err != nil {
		return ImageRef{}, fmt.Errorf("image load: read first entry: %w", err)
	}
	if err := checkTarEntry(hdr); err != nil {
		return ImageRef{}, fmt.Errorf("image load: %w", err)
	}
	if hdr.Name != "manifest.json" {
		return ImageRef{}, fmt.Errorf("image load: first tar entry is %q, want manifest.json", hdr.Name)
	}
	manifestBytes, err := io.ReadAll(io.LimitReader(tr, 1<<20)) // 1 MiB cap on manifest
	if err != nil {
		return ImageRef{}, fmt.Errorf("image load: read manifest: %w", err)
	}
	var m ImageManifest
	if err := json.Unmarshal(manifestBytes, &m); err != nil {
		return ImageRef{}, fmt.Errorf("image load: parse manifest: %w", err)
	}

	refSpec := m.Name + ":" + m.Tag
	if overrideTag != "" {
		refSpec = overrideTag
	}
	ref, err := ParseImageRef(refSpec)
	if err != nil {
		return ImageRef{}, fmt.Errorf("image load: invalid ref %q: %w", refSpec, err)
	}

	dstDir := ref.Path()
	if ImageExists(ref) && !force {
		return ImageRef{}, fmt.Errorf("image load: %s already exists (use -force to overwrite)", ref)
	}

	tmpDir := dstDir + ".tmp"
	if err := os.RemoveAll(tmpDir); err != nil {
		return ImageRef{}, fmt.Errorf("image load: clear staging: %w", err)
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return ImageRef{}, fmt.Errorf("image load: mkdir staging: %w", err)
	}
	cleanup := func() { os.RemoveAll(tmpDir) }
	committed := false
	defer func() {
		if !committed {
			cleanup()
		}
	}()

	// Persist the (possibly-renamed) manifest first so the on-disk Name/Tag
	// agree with the ref we write to disk under.
	if overrideTag != "" {
		m.Name = ref.Name
		m.Tag = ref.Tag
		manifestBytes, err = json.MarshalIndent(&m, "", "  ")
		if err != nil {
			return ImageRef{}, fmt.Errorf("image load: re-marshal manifest: %w", err)
		}
		manifestBytes = append(manifestBytes, '\n')
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "manifest.json"), manifestBytes, 0o644); err != nil {
		return ImageRef{}, fmt.Errorf("image load: write manifest: %w", err)
	}

	wantData := map[string]bool{}
	for _, name := range imageDataFiles {
		wantData[name] = true
	}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ImageRef{}, fmt.Errorf("image load: read tar: %w", err)
		}
		if err := checkTarEntry(hdr); err != nil {
			return ImageRef{}, fmt.Errorf("image load: %w", err)
		}
		if !wantData[hdr.Name] {
			return ImageRef{}, fmt.Errorf("image load: unexpected tar entry %q", hdr.Name)
		}
		dst := filepath.Join(tmpDir, hdr.Name)
		if !strings.HasPrefix(filepath.Clean(dst)+string(os.PathSeparator), filepath.Clean(tmpDir)+string(os.PathSeparator)) {
			return ImageRef{}, fmt.Errorf("image load: entry %q escapes destination", hdr.Name)
		}
		out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return ImageRef{}, fmt.Errorf("image load: open %s: %w", hdr.Name, err)
		}
		if _, err := io.Copy(out, io.LimitReader(tr, imageEntryMaxBytes+1)); err != nil {
			out.Close()
			return ImageRef{}, fmt.Errorf("image load: write %s: %w", hdr.Name, err)
		}
		if err := out.Close(); err != nil {
			return ImageRef{}, fmt.Errorf("image load: close %s: %w", hdr.Name, err)
		}
		st, err := os.Stat(dst)
		if err != nil {
			return ImageRef{}, fmt.Errorf("image load: stat %s: %w", hdr.Name, err)
		}
		if st.Size() > imageEntryMaxBytes {
			return ImageRef{}, fmt.Errorf("image load: %s exceeds %d byte cap", hdr.Name, imageEntryMaxBytes)
		}
		delete(wantData, hdr.Name)
	}
	if len(wantData) > 0 {
		missing := make([]string, 0, len(wantData))
		for n := range wantData {
			missing = append(missing, n)
		}
		return ImageRef{}, fmt.Errorf("image load: tar missing required files: %s", strings.Join(missing, ", "))
	}

	if force && ImageExists(ref) {
		if err := os.RemoveAll(dstDir); err != nil {
			return ImageRef{}, fmt.Errorf("image load: remove existing: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(dstDir), 0o755); err != nil {
		return ImageRef{}, fmt.Errorf("image load: mkdir parent: %w", err)
	}
	if err := os.Rename(tmpDir, dstDir); err != nil {
		return ImageRef{}, fmt.Errorf("image load: rename: %w", err)
	}
	committed = true
	return ref, nil
}

// checkTarEntry rejects unsafe tar entries before any filesystem write.
// Strict by design: only regular files with simple base names; any
// `..`, leading `/`, separator, symlink, or non-regular typeflag is
// rejected outright.
func checkTarEntry(hdr *tar.Header) error {
	if hdr == nil {
		return errors.New("nil tar header")
	}
	if hdr.Typeflag != tar.TypeReg {
		return fmt.Errorf("entry %q has disallowed typeflag %v", hdr.Name, hdr.Typeflag)
	}
	name := hdr.Name
	if name == "" {
		return errors.New("empty tar entry name")
	}
	if filepath.IsAbs(name) || strings.HasPrefix(name, "/") {
		return fmt.Errorf("entry %q has absolute path", name)
	}
	if strings.ContainsRune(name, os.PathSeparator) || strings.Contains(name, "/") {
		return fmt.Errorf("entry %q contains path separator", name)
	}
	cleaned := filepath.Clean(name)
	if cleaned != name || cleaned == ".." || strings.Contains(cleaned, "..") {
		return fmt.Errorf("entry %q is unsafe", name)
	}
	if hdr.Linkname != "" {
		return fmt.Errorf("entry %q has linkname (symlinks rejected)", name)
	}
	if hdr.Size < 0 {
		return fmt.Errorf("entry %q has negative size", name)
	}
	if hdr.Size > imageEntryMaxBytes {
		return fmt.Errorf("entry %q exceeds %d byte cap (size=%d)", name, imageEntryMaxBytes, hdr.Size)
	}
	return nil
}
