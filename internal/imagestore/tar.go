package imagestore

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// LayerFiles is the deterministic write order for the image sidecar files.
// Tar and registry transports fail loud if any layer is missing.
var LayerFiles = []string{"disk.img", "aux.img", "hw.model", "machine.id"}

// MaxEntryBytes caps a single image transport entry. Cove disk images can be
// many tens of GB, but larger entries are treated as malicious or corrupt input.
const MaxEntryBytes int64 = 100 * 1024 * 1024 * 1024

// WriteTarFile tars an image directory to dst. It writes dst+".tmp", renames
// on success, and removes the temp file on error.
func WriteTarFile(ref Ref, dst string, gzipOut bool) error {
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
	if err := WriteTar(ref, out, gzipOut); err != nil {
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

// WriteTar streams an image directory tarball to w.
func WriteTar(ref Ref, w io.Writer, gzipOut bool) error {
	if !Exists(ref) {
		return fmt.Errorf("image push: %s not found in store", ref)
	}
	imgDir := ref.Path()
	manifestPath := filepath.Join(imgDir, "manifest.json")
	if _, err := os.Stat(manifestPath); err != nil {
		return fmt.Errorf("image push: manifest missing: %w", err)
	}
	for _, name := range LayerFiles {
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
	for _, name := range LayerFiles {
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

// LoadTarFromFile extracts a tarball into the local image store.
func LoadTarFromFile(src, overrideTag string, force bool) (Ref, error) {
	f, err := os.Open(src)
	if err != nil {
		return Ref{}, fmt.Errorf("image load: open %s: %w", src, err)
	}
	defer f.Close()

	var r io.Reader = f
	if strings.HasSuffix(strings.ToLower(src), ".gz") || strings.HasSuffix(strings.ToLower(src), ".tgz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return Ref{}, fmt.Errorf("image load: gzip: %w", err)
		}
		defer gz.Close()
		r = gz
	}
	return readTarStream(r, overrideTag, force)
}

// ReadTar extracts an image tarball from r into the local image store.
func ReadTar(r io.Reader, overrideTag string, force bool) (Ref, error) {
	br, err := maybeGunzip(r)
	if err != nil {
		return Ref{}, err
	}
	return readTarStream(br, overrideTag, force)
}

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

func readTarStream(r io.Reader, overrideTag string, force bool) (Ref, error) {
	tr := tar.NewReader(r)

	hdr, err := tr.Next()
	if err != nil {
		return Ref{}, fmt.Errorf("image load: read first entry: %w", err)
	}
	if err := checkTarEntry(hdr); err != nil {
		return Ref{}, fmt.Errorf("image load: %w", err)
	}
	if hdr.Name != "manifest.json" {
		return Ref{}, fmt.Errorf("image load: first tar entry is %q, want manifest.json", hdr.Name)
	}
	manifestBytes, err := io.ReadAll(io.LimitReader(tr, 1<<20))
	if err != nil {
		return Ref{}, fmt.Errorf("image load: read manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(manifestBytes, &m); err != nil {
		return Ref{}, fmt.Errorf("image load: parse manifest: %w", err)
	}

	refSpec := m.Name + ":" + m.Tag
	if overrideTag != "" {
		refSpec = overrideTag
	}
	ref, err := ParseRef(refSpec)
	if err != nil {
		return Ref{}, fmt.Errorf("image load: invalid ref %q: %w", refSpec, err)
	}

	dstDir := ref.Path()
	if Exists(ref) && !force {
		return Ref{}, fmt.Errorf("image load: %s already exists (use -force to overwrite)", ref)
	}

	tmpDir := dstDir + ".tmp"
	if err := os.RemoveAll(tmpDir); err != nil {
		return Ref{}, fmt.Errorf("image load: clear staging: %w", err)
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return Ref{}, fmt.Errorf("image load: mkdir staging: %w", err)
	}
	cleanup := func() { os.RemoveAll(tmpDir) }
	committed := false
	defer func() {
		if !committed {
			cleanup()
		}
	}()

	if overrideTag != "" {
		m.Name = ref.Name
		m.Tag = ref.Tag
		manifestBytes, err = json.MarshalIndent(&m, "", "  ")
		if err != nil {
			return Ref{}, fmt.Errorf("image load: re-marshal manifest: %w", err)
		}
		manifestBytes = append(manifestBytes, '\n')
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "manifest.json"), manifestBytes, 0o644); err != nil {
		return Ref{}, fmt.Errorf("image load: write manifest: %w", err)
	}

	wantData := map[string]bool{}
	for _, name := range LayerFiles {
		wantData[name] = true
	}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Ref{}, fmt.Errorf("image load: read tar: %w", err)
		}
		if err := checkTarEntry(hdr); err != nil {
			return Ref{}, fmt.Errorf("image load: %w", err)
		}
		if !wantData[hdr.Name] {
			return Ref{}, fmt.Errorf("image load: unexpected tar entry %q", hdr.Name)
		}
		dst := filepath.Join(tmpDir, hdr.Name)
		if !strings.HasPrefix(filepath.Clean(dst)+string(os.PathSeparator), filepath.Clean(tmpDir)+string(os.PathSeparator)) {
			return Ref{}, fmt.Errorf("image load: entry %q escapes destination", hdr.Name)
		}
		out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return Ref{}, fmt.Errorf("image load: open %s: %w", hdr.Name, err)
		}
		if _, err := io.Copy(out, io.LimitReader(tr, MaxEntryBytes+1)); err != nil {
			out.Close()
			return Ref{}, fmt.Errorf("image load: write %s: %w", hdr.Name, err)
		}
		if err := out.Close(); err != nil {
			return Ref{}, fmt.Errorf("image load: close %s: %w", hdr.Name, err)
		}
		st, err := os.Stat(dst)
		if err != nil {
			return Ref{}, fmt.Errorf("image load: stat %s: %w", hdr.Name, err)
		}
		if st.Size() > MaxEntryBytes {
			return Ref{}, fmt.Errorf("image load: %s exceeds %d byte cap", hdr.Name, MaxEntryBytes)
		}
		delete(wantData, hdr.Name)
	}
	if len(wantData) > 0 {
		missing := make([]string, 0, len(wantData))
		for n := range wantData {
			missing = append(missing, n)
		}
		return Ref{}, fmt.Errorf("image load: tar missing required files: %s", strings.Join(missing, ", "))
	}

	if force && Exists(ref) {
		if err := os.RemoveAll(dstDir); err != nil {
			return Ref{}, fmt.Errorf("image load: remove existing: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(dstDir), 0o755); err != nil {
		return Ref{}, fmt.Errorf("image load: mkdir parent: %w", err)
	}
	if err := os.Rename(tmpDir, dstDir); err != nil {
		return Ref{}, fmt.Errorf("image load: rename: %w", err)
	}
	committed = true
	return ref, nil
}

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
	if hdr.Size > MaxEntryBytes {
		return fmt.Errorf("entry %q exceeds %d byte cap (size=%d)", name, MaxEntryBytes, hdr.Size)
	}
	return nil
}
