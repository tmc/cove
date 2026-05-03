package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildSampleImage stages a fake VM and runs BuildImage so we have a real
// on-disk image to push. Returns the ref of the built image.
func buildSampleImage(t *testing.T, vmName, refSpec string) ImageRef {
	t.Helper()
	stageMacOSVMForImage(t, vmName)
	ref, err := ParseImageRef(refSpec)
	if err != nil {
		t.Fatalf("ParseImageRef(%q): %v", refSpec, err)
	}
	if _, err := BuildImage(BuildImageOptions{SourceVM: vmName, Ref: ref}); err != nil {
		t.Fatalf("BuildImage: %v", err)
	}
	return ref
}

func TestPushImage_RoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ref := buildSampleImage(t, "src", "trip:v1")
	tarPath := filepath.Join(t.TempDir(), "trip.tar")
	if err := PushImageToFile(ref, tarPath, false); err != nil {
		t.Fatalf("PushImageToFile: %v", err)
	}

	// Capture original bytes for comparison.
	originals := map[string][]byte{}
	for _, name := range append([]string{"manifest.json"}, imageDataFiles...) {
		b, err := os.ReadFile(filepath.Join(ref.Path(), name))
		if err != nil {
			t.Fatalf("read original %s: %v", name, err)
		}
		originals[name] = b
	}

	// Wipe and reload.
	if err := os.RemoveAll(ref.Path()); err != nil {
		t.Fatalf("remove image: %v", err)
	}
	loaded, err := LoadImageFromFile(tarPath, "", false)
	if err != nil {
		t.Fatalf("LoadImageFromFile: %v", err)
	}
	if loaded.String() != ref.String() {
		t.Errorf("loaded ref = %s, want %s", loaded, ref)
	}
	for name, want := range originals {
		got, err := os.ReadFile(filepath.Join(loaded.Path(), name))
		if err != nil {
			t.Errorf("read loaded %s: %v", name, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s differs after round-trip", name)
		}
	}
}

func TestLoadImage_RegistersInStore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ref := buildSampleImage(t, "src", "registered:v1")
	tarPath := filepath.Join(t.TempDir(), "img.tar")
	if err := PushImageToFile(ref, tarPath, false); err != nil {
		t.Fatalf("PushImageToFile: %v", err)
	}
	if err := os.RemoveAll(ref.Path()); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if _, err := LoadImageFromFile(tarPath, "", false); err != nil {
		t.Fatalf("LoadImageFromFile: %v", err)
	}
	entries, err := ListImages()
	if err != nil {
		t.Fatalf("ListImages: %v", err)
	}
	found := false
	for _, e := range entries {
		if e.Ref.String() == "registered:v1" {
			found = true
		}
	}
	if !found {
		t.Errorf("ListImages missing registered:v1 (got %d entries)", len(entries))
	}
}

func TestPushImage_RefusesNonexistent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ref, _ := ParseImageRef("ghost:v1")
	err := PushImageToFile(ref, filepath.Join(t.TempDir(), "out.tar"), false)
	if err == nil {
		t.Fatal("PushImageToFile on missing image succeeded; want error")
	}
	if !strings.Contains(err.Error(), "ghost:v1") {
		t.Errorf("error %q does not mention ghost:v1", err)
	}
}

func TestLoadImage_RefusesDuplicateTag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ref := buildSampleImage(t, "src", "dup:v1")
	tarPath := filepath.Join(t.TempDir(), "dup.tar")
	if err := PushImageToFile(ref, tarPath, false); err != nil {
		t.Fatalf("PushImageToFile: %v", err)
	}
	// Existing image still in store; load should refuse without -force.
	if _, err := LoadImageFromFile(tarPath, "", false); err == nil {
		t.Fatal("LoadImageFromFile over existing succeeded; want refuse")
	} else if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error %q does not mention already-exists", err)
	}
	// With -force, load succeeds.
	if _, err := LoadImageFromFile(tarPath, "", true); err != nil {
		t.Fatalf("LoadImageFromFile -force: %v", err)
	}
}

// writeRawTar is a test helper that writes a hand-crafted tar to dst.
// The first entry is always a valid manifest.json so load gets past the
// initial check; subsequent entries are the malicious payload under test.
func writeRawTar(t *testing.T, dst string, entries []tar.Header, bodies map[string][]byte) {
	t.Helper()
	f, err := os.Create(dst)
	if err != nil {
		t.Fatalf("create %s: %v", dst, err)
	}
	defer f.Close()
	tw := tar.NewWriter(f)
	manifestBody := []byte(`{"schemaVersion":1,"name":"evil","tag":"v1","diskSHA256":"x","diskSize":0,"createdAt":"2026-05-02T00:00:00Z"}`)
	if err := tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0o644, Size: int64(len(manifestBody)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatalf("manifest header: %v", err)
	}
	if _, err := tw.Write(manifestBody); err != nil {
		t.Fatalf("manifest body: %v", err)
	}
	for _, h := range entries {
		hdr := h
		body := bodies[hdr.Name]
		hdr.Size = int64(len(body))
		if err := tw.WriteHeader(&hdr); err != nil {
			t.Fatalf("write header %q: %v", hdr.Name, err)
		}
		if len(body) > 0 {
			if _, err := tw.Write(body); err != nil {
				t.Fatalf("write body %q: %v", hdr.Name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
}

func TestLoadImage_RejectsZipSlip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tarPath := filepath.Join(t.TempDir(), "evil.tar")
	writeRawTar(t, tarPath, []tar.Header{
		{Name: "../etc/passwd", Mode: 0o644, Typeflag: tar.TypeReg},
	}, map[string][]byte{
		"../etc/passwd": []byte("pwned"),
	})
	_, err := LoadImageFromFile(tarPath, "", false)
	if err == nil {
		t.Fatal("LoadImageFromFile accepted zip-slip entry; want error")
	}
	if !strings.Contains(err.Error(), "..") && !strings.Contains(err.Error(), "separator") && !strings.Contains(err.Error(), "unsafe") {
		t.Errorf("error %q does not flag the offending name", err)
	}
}

func TestLoadImage_RejectsSymlink(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tarPath := filepath.Join(t.TempDir(), "symlink.tar")
	// Build a tar with one symlink entry. We can't use writeRawTar (it
	// only emits TypeReg); inline the construction.
	f, err := os.Create(tarPath)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	tw := tar.NewWriter(f)
	manifestBody := []byte(`{"schemaVersion":1,"name":"sym","tag":"v1","diskSHA256":"x","diskSize":0,"createdAt":"2026-05-02T00:00:00Z"}`)
	if err := tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0o644, Size: int64(len(manifestBody)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatalf("manifest header: %v", err)
	}
	if _, err := tw.Write(manifestBody); err != nil {
		t.Fatalf("manifest body: %v", err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: "disk.img", Linkname: "/etc/passwd", Mode: 0o644, Typeflag: tar.TypeSymlink}); err != nil {
		t.Fatalf("symlink header: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close file: %v", err)
	}
	_, err = LoadImageFromFile(tarPath, "", false)
	if err == nil {
		t.Fatal("LoadImageFromFile accepted symlink entry; want error")
	}
	if !strings.Contains(err.Error(), "typeflag") && !strings.Contains(err.Error(), "symlink") {
		t.Errorf("error %q does not flag the symlink", err)
	}
}

func TestPushImage_GzipRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ref := buildSampleImage(t, "src", "gz:v1")
	rawPath := filepath.Join(t.TempDir(), "raw.tar")
	gzPath := filepath.Join(t.TempDir(), "gz.tar.gz")
	if err := PushImageToFile(ref, rawPath, false); err != nil {
		t.Fatalf("PushImageToFile raw: %v", err)
	}
	if err := PushImageToFile(ref, gzPath, true); err != nil {
		t.Fatalf("PushImageToFile gzip: %v", err)
	}
	rawSize := mustStatSize(t, rawPath)
	gzSize := mustStatSize(t, gzPath)
	if gzSize >= rawSize {
		t.Errorf("gzip size %d not smaller than raw %d (small fixtures may compress poorly; investigate)", gzSize, rawSize)
	}
	// Confirm the gzip output really starts with the magic bytes.
	head, err := os.ReadFile(gzPath)
	if err != nil {
		t.Fatalf("read gz: %v", err)
	}
	if len(head) < 2 || head[0] != 0x1f || head[1] != 0x8b {
		t.Errorf("gz output missing gzip magic")
	}
	// Sanity: gzip stream is parseable.
	gzReader, err := gzip.NewReader(bytes.NewReader(head))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	gzReader.Close()

	// Wipe and load the gzipped tar back.
	if err := os.RemoveAll(ref.Path()); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	loaded, err := LoadImageFromFile(gzPath, "", false)
	if err != nil {
		t.Fatalf("LoadImageFromFile gzip: %v", err)
	}
	if loaded.String() != "gz:v1" {
		t.Errorf("loaded.String() = %s, want gz:v1", loaded)
	}
	for _, name := range imageDataFiles {
		if _, err := os.Stat(filepath.Join(loaded.Path(), name)); err != nil {
			t.Errorf("loaded image missing %s: %v", name, err)
		}
	}
}

func TestLoadImage_RenameViaTagFlag(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ref := buildSampleImage(t, "src", "orig:v1")
	tarPath := filepath.Join(t.TempDir(), "orig.tar")
	if err := PushImageToFile(ref, tarPath, false); err != nil {
		t.Fatalf("PushImageToFile: %v", err)
	}
	// Wipe original and load under a different ref.
	if err := os.RemoveAll(ref.Path()); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	loaded, err := LoadImageFromFile(tarPath, "renamed:v2", false)
	if err != nil {
		t.Fatalf("LoadImageFromFile -tag: %v", err)
	}
	if loaded.String() != "renamed:v2" {
		t.Errorf("loaded.String() = %s, want renamed:v2", loaded)
	}
	manifest, err := LoadImageManifest(loaded)
	if err != nil {
		t.Fatalf("LoadImageManifest: %v", err)
	}
	if manifest.Name != "renamed" || manifest.Tag != "v2" {
		t.Errorf("manifest Name/Tag = %s/%s, want renamed/v2", manifest.Name, manifest.Tag)
	}
}

func mustStatSize(t *testing.T, path string) int64 {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return st.Size()
}

// TestWriteImageTar_StdoutRoundTrip drives the stdin/stdout streaming
// path: WriteImageTar -> bytes.Buffer -> ReadImageTar. This is the
// in-process equivalent of `cove image push x:1 - | cove image load -`.
// TTY refusal is enforced one layer up (in runImagePush / runImageLoad);
// not unit-tested here because term.IsTerminal(int) requires a real fd.
func TestWriteImageTar_StdoutRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ref := buildSampleImage(t, "src", "stream:v1")

	originals := map[string][]byte{}
	for _, name := range append([]string{"manifest.json"}, imageDataFiles...) {
		b, err := os.ReadFile(filepath.Join(ref.Path(), name))
		if err != nil {
			t.Fatalf("read original %s: %v", name, err)
		}
		originals[name] = b
	}

	var buf bytes.Buffer
	if err := WriteImageTar(ref, &buf, false); err != nil {
		t.Fatalf("WriteImageTar: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("WriteImageTar produced 0 bytes")
	}

	if err := os.RemoveAll(ref.Path()); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	loaded, err := ReadImageTar(&buf, "", false)
	if err != nil {
		t.Fatalf("ReadImageTar: %v", err)
	}
	if loaded.String() != ref.String() {
		t.Errorf("loaded ref = %s, want %s", loaded, ref)
	}
	for name, want := range originals {
		got, err := os.ReadFile(filepath.Join(loaded.Path(), name))
		if err != nil {
			t.Errorf("read loaded %s: %v", name, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s differs after stream round-trip", name)
		}
	}
}

// TestWriteImageTar_GzipStreamRoundTrip checks that gzip framing also
// survives a memory-backed round-trip. The receiver's gzip detection is
// magic-byte sniffing (not filename suffix) so this is the only path
// where the auto-gunzip in maybeGunzip gets exercised.
func TestWriteImageTar_GzipStreamRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ref := buildSampleImage(t, "src", "gzstream:v1")

	var buf bytes.Buffer
	if err := WriteImageTar(ref, &buf, true); err != nil {
		t.Fatalf("WriteImageTar gzip: %v", err)
	}
	head := buf.Bytes()
	if len(head) < 2 || head[0] != 0x1f || head[1] != 0x8b {
		t.Fatal("WriteImageTar -gzip output missing gzip magic")
	}

	if err := os.RemoveAll(ref.Path()); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	loaded, err := ReadImageTar(&buf, "", false)
	if err != nil {
		t.Fatalf("ReadImageTar gzip: %v", err)
	}
	if loaded.String() != "gzstream:v1" {
		t.Errorf("loaded.String() = %s, want gzstream:v1", loaded)
	}
	for _, name := range imageDataFiles {
		if _, err := os.Stat(filepath.Join(loaded.Path(), name)); err != nil {
			t.Errorf("loaded image missing %s: %v", name, err)
		}
	}
}
