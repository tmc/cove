package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/cove/internal/vmconfig"
)

func TestProvisioningSourceHash(t *testing.T) {
	h1 := ProvisioningSourceHash()
	if len(h1) != 12 {
		t.Fatalf("hash length = %d, want 12", len(h1))
	}

	// Hash should be deterministic.
	h2 := ProvisioningSourceHash()
	if h1 != h2 {
		t.Errorf("hash not deterministic: %q != %q", h1, h2)
	}
}

func TestCheckTemplateStaleNoHash(t *testing.T) {
	dir := t.TempDir()
	stale, tmplHash, curHash := CheckTemplateStale(dir)
	if stale {
		t.Error("expected not stale when no hash file exists")
	}
	if tmplHash != "" || curHash != "" {
		t.Errorf("expected empty hashes, got tmpl=%q cur=%q", tmplHash, curHash)
	}
}

func TestCheckTemplateStaleWithHash(t *testing.T) {
	dir := t.TempDir()
	hashPath := filepath.Join(dir, TemplateHashFile)

	// Matching hash: not stale.
	if err := os.WriteFile(hashPath, []byte(ProvisioningSourceHash()), 0644); err != nil {
		t.Fatal(err)
	}
	if stale, _, _ := CheckTemplateStale(dir); stale {
		t.Error("expected not stale when hashes match")
	}

	// Mismatched hash: stale, both hashes returned.
	if err := os.WriteFile(hashPath, []byte("deadbeef0000"), 0644); err != nil {
		t.Fatal(err)
	}
	stale, tmplHash, curHash := CheckTemplateStale(dir)
	if !stale {
		t.Error("expected stale when hashes differ")
	}
	if tmplHash != "deadbeef0000" || curHash == "" {
		t.Errorf("hashes: tmpl=%q cur=%q", tmplHash, curHash)
	}
}

// TestTemplateErrorPaths covers error returns from SaveTemplate,
// CreateFromTemplate, and DeleteTemplate without exercising the
// disk-image copy/compress paths.
func TestTemplateErrorPaths(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// SaveTemplate: source VM does not exist.
	if err := SaveTemplate("nope", "tpl"); !errors.Is(err, ErrTemplateSourceNotFound) {
		t.Errorf("SaveTemplate missing VM: got %v, want ErrTemplateSourceNotFound", err)
	}

	// DeleteTemplate: template does not exist.
	if err := DeleteTemplate("ghost"); !errors.Is(err, ErrTemplateNotFound) {
		t.Errorf("DeleteTemplate missing: got %v, want ErrTemplateNotFound", err)
	}

	// CreateFromTemplate: template does not exist.
	if err := CreateFromTemplate("ghost", "newvm"); !errors.Is(err, ErrTemplateNotFound) {
		t.Errorf("CreateFromTemplate missing: got %v, want ErrTemplateNotFound", err)
	}

	// SaveTemplate: template already exists. Stage a minimally-valid
	// source VM and a pre-existing template directory.
	vmPath := vmconfig.Path("src")
	if err := os.MkdirAll(vmPath, 0755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"disk.img", "aux.img"} {
		if err := os.WriteFile(filepath.Join(vmPath, f), nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	tplDir := filepath.Join(vmconfig.TemplateDir(), "dup")
	if err := os.MkdirAll(tplDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := SaveTemplate("src", "dup"); !errors.Is(err, ErrTemplateExists) {
		t.Errorf("SaveTemplate dup: got %v, want ErrTemplateExists", err)
	}
}

func TestCompressDecompressRoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.bin")
	gz := filepath.Join(dir, "src.bin.gz")
	out := filepath.Join(dir, "out.bin")

	want := make([]byte, 64*1024)
	for i := range want {
		want[i] = byte(i % 251)
	}
	if err := os.WriteFile(src, want, 0644); err != nil {
		t.Fatal(err)
	}

	if err := compressFile(src, gz); err != nil {
		t.Fatalf("compressFile: %v", err)
	}
	gzInfo, err := os.Stat(gz)
	if err != nil {
		t.Fatal(err)
	}
	if gzInfo.Size() == 0 {
		t.Fatal("compressed file is empty")
	}

	if err := decompressFile(gz, out); err != nil {
		t.Fatalf("decompressFile: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("size: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("byte %d: got %d, want %d", i, got[i], want[i])
		}
	}
}

func TestCompressFileErrors(t *testing.T) {
	dir := t.TempDir()
	if err := compressFile(filepath.Join(dir, "missing"), filepath.Join(dir, "out.gz")); err == nil {
		t.Error("compressFile missing src: want error")
	}
	src := filepath.Join(dir, "src")
	if err := os.WriteFile(src, []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := compressFile(src, filepath.Join(dir, "no-such-dir", "out.gz")); err == nil {
		t.Error("compressFile bad dst: want error")
	}
}

func TestDecompressFileErrors(t *testing.T) {
	dir := t.TempDir()
	if err := decompressFile(filepath.Join(dir, "missing"), filepath.Join(dir, "out")); err == nil {
		t.Error("decompressFile missing src: want error")
	}
	bad := filepath.Join(dir, "bad.gz")
	if err := os.WriteFile(bad, []byte("not gzip"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := decompressFile(bad, filepath.Join(dir, "out")); err == nil {
		t.Error("decompressFile non-gzip: want error")
	}
}

func TestGetTemplateInfoFastAndCompressed(t *testing.T) {
	dir := t.TempDir()

	// Missing required file -> error.
	if _, err := getTemplateInfo(dir); err == nil {
		t.Error("getTemplateInfo empty dir: want error")
	}

	// Compressed mode: stage required files.
	for _, f := range TemplateFiles {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	info, err := getTemplateInfo(dir)
	if err != nil {
		t.Fatalf("getTemplateInfo compressed: %v", err)
	}
	if info.FastMode {
		t.Error("expected FastMode=false")
	}
	if info.Name != filepath.Base(dir) {
		t.Errorf("Name=%q, want %q", info.Name, filepath.Base(dir))
	}

	// Fast mode: marker present, requires disk.img.
	fast := t.TempDir()
	if err := os.WriteFile(filepath.Join(fast, TemplateMarkerFast), nil, 0644); err != nil {
		t.Fatal(err)
	}
	for _, f := range TemplateFilesFast {
		if err := os.WriteFile(filepath.Join(fast, f), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	info, err = getTemplateInfo(fast)
	if err != nil {
		t.Fatalf("getTemplateInfo fast: %v", err)
	}
	if !info.FastMode {
		t.Error("expected FastMode=true")
	}
}

func TestListTemplatesEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	got, err := ListTemplates()
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d templates, want 0", len(got))
	}
	// Directory should have been created.
	if _, err := os.Stat(vmconfig.TemplateDir()); err != nil {
		t.Errorf("template dir not created: %v", err)
	}
}

func TestDeleteTemplateRemovesFastModeTemplate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tplPath := filepath.Join(vmconfig.TemplateDir(), "fast-tpl")
	if err := os.MkdirAll(tplPath, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, f := range []string{TemplateMarkerFast, "disk.img", "aux.img", "hw.model"} {
		if err := os.WriteFile(filepath.Join(tplPath, f), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}
	if err := DeleteTemplate("fast-tpl"); err != nil {
		t.Fatalf("DeleteTemplate: %v", err)
	}
	if _, err := os.Stat(tplPath); !os.IsNotExist(err) {
		t.Fatalf("template still exists: err=%v", err)
	}
}
