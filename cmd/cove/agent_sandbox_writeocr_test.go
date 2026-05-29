package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteOCRTextMissingDirWritesPlaceholder(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "ocr.txt")
	missing := filepath.Join(dir, "no-such-screenshots")

	if err := writeOCRText(out, missing); err != nil {
		t.Fatalf("writeOCRText: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "(no screenshots)\n" {
		t.Errorf("output = %q, want %q", got, "(no screenshots)\n")
	}
}

func TestWriteOCRTextReadDirErrorWraps(t *testing.T) {
	dir := t.TempDir()
	// Create a regular file and pass it as the "screenshots dir" — ReadDir
	// will return a non-IsNotExist error that should be wrapped, not silently
	// converted to a placeholder write.
	notADir := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	err := writeOCRText(filepath.Join(dir, "ocr.txt"), notADir)
	if err == nil {
		t.Fatal("writeOCRText returned nil error for non-directory screenshots path; want wrapped read error")
	}
}

func TestWriteOCRTextDirWithOnlyNonPNGs(t *testing.T) {
	dir := t.TempDir()
	screenshots := filepath.Join(dir, "shots")
	if err := os.MkdirAll(screenshots, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A non-PNG file (and a subdirectory) — both must be skipped, leaving
	// the per-image loop with zero output, which falls through to the
	// "(no screenshots)\n" placeholder write.
	if err := os.WriteFile(filepath.Join(screenshots, "notes.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(screenshots, "subdir"), 0o755); err != nil {
		t.Fatalf("subdir: %v", err)
	}

	out := filepath.Join(dir, "ocr.txt")
	if err := writeOCRText(out, screenshots); err != nil {
		t.Fatalf("writeOCRText: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "(no screenshots)\n" {
		t.Errorf("output = %q, want placeholder", got)
	}
}
