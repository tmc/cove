package imagestore

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func stageTarTestImage(t *testing.T, refSpec string) Ref {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	ref, err := ParseRef(refSpec)
	if err != nil {
		t.Fatalf("ParseRef(%q): %v", refSpec, err)
	}
	if err := os.MkdirAll(ref.Path(), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	manifest := &Manifest{
		SchemaVersion: 1,
		Name:          ref.Name,
		Tag:           ref.Tag,
		OSType:        "darwin",
		DiskSHA256:    strings.Repeat("a", 64),
		DiskSize:      4,
		CreatedAt:     time.Unix(1, 0),
	}
	if err := WriteManifest(ref.Path(), manifest); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	for _, name := range LayerFiles {
		if err := os.WriteFile(filepath.Join(ref.Path(), name), []byte("data-"+name), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return ref
}

func TestWriteTarFileRoundTrip(t *testing.T) {
	ref := stageTarTestImage(t, "trip:v1")
	tarPath := filepath.Join(t.TempDir(), "trip.tar")
	if err := WriteTarFile(ref, tarPath, false); err != nil {
		t.Fatalf("WriteTarFile: %v", err)
	}
	if err := os.RemoveAll(ref.Path()); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	loaded, err := LoadTarFromFile(tarPath, "", false)
	if err != nil {
		t.Fatalf("LoadTarFromFile: %v", err)
	}
	if loaded != ref {
		t.Fatalf("loaded ref = %s, want %s", loaded, ref)
	}
	for _, name := range append([]string{"manifest.json"}, LayerFiles...) {
		if _, err := os.Stat(filepath.Join(loaded.Path(), name)); err != nil {
			t.Fatalf("loaded missing %s: %v", name, err)
		}
	}
}

func TestWriteTarRejectsMissingLayer(t *testing.T) {
	ref := stageTarTestImage(t, "missing:v1")
	if err := os.Remove(filepath.Join(ref.Path(), "disk.img")); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := WriteTar(ref, &bytes.Buffer{}, false); err == nil || !strings.Contains(err.Error(), "source missing disk.img") {
		t.Fatalf("WriteTar error = %v, want missing disk.img", err)
	}
}

func TestReadTarGzipRoundTrip(t *testing.T) {
	ref := stageTarTestImage(t, "gzip:v1")
	var buf bytes.Buffer
	if err := WriteTar(ref, &buf, true); err != nil {
		t.Fatalf("WriteTar gzip: %v", err)
	}
	if got := buf.Bytes(); len(got) < 2 || got[0] != 0x1f || got[1] != 0x8b {
		t.Fatal("WriteTar gzip output missing gzip magic")
	}
	if err := os.RemoveAll(ref.Path()); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	loaded, err := ReadTar(&buf, "", false)
	if err != nil {
		t.Fatalf("ReadTar: %v", err)
	}
	if loaded != ref {
		t.Fatalf("loaded ref = %s, want %s", loaded, ref)
	}
}

func TestLoadTarOverrideTag(t *testing.T) {
	ref := stageTarTestImage(t, "original:v1")
	tarPath := filepath.Join(t.TempDir(), "image.tar")
	if err := WriteTarFile(ref, tarPath, false); err != nil {
		t.Fatalf("WriteTarFile: %v", err)
	}
	loaded, err := LoadTarFromFile(tarPath, "renamed:v2", false)
	if err != nil {
		t.Fatalf("LoadTarFromFile override: %v", err)
	}
	if loaded.String() != "renamed:v2" {
		t.Fatalf("loaded = %s, want renamed:v2", loaded)
	}
}

func TestCheckTarEntryRejections(t *testing.T) {
	tests := []struct {
		name    string
		hdr     *tar.Header
		wantSub string
	}{
		{"nil", nil, "nil tar header"},
		{"symlink", &tar.Header{Name: "link", Typeflag: tar.TypeSymlink}, "disallowed typeflag"},
		{"empty", &tar.Header{Name: "", Typeflag: tar.TypeReg}, "empty tar entry name"},
		{"absolute", &tar.Header{Name: "/abs", Typeflag: tar.TypeReg}, "absolute path"},
		{"separator", &tar.Header{Name: "sub/x", Typeflag: tar.TypeReg}, "path separator"},
		{"dotdot", &tar.Header{Name: "..", Typeflag: tar.TypeReg}, "unsafe"},
		{"linkname", &tar.Header{Name: "ok", Typeflag: tar.TypeReg, Linkname: "x"}, "linkname"},
		{"negativeSize", &tar.Header{Name: "ok", Typeflag: tar.TypeReg, Size: -1}, "negative size"},
		{"oversize", &tar.Header{Name: "disk.img", Typeflag: tar.TypeReg, Size: MaxEntryBytes + 1}, "exceeds"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkTarEntry(tt.hdr)
			if err == nil || !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("err = %v, want %q", err, tt.wantSub)
			}
		})
	}
}
