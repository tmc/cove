package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

// TestRenameVM_NotFound asserts that renaming a non-existent VM fails
// rather than silently creating an empty target.
func TestRenameVM_NotFound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	err := RenameVM("ghost", "newghost")
	if err == nil {
		t.Fatal("RenameVM(ghost, newghost) returned nil; want not-found error")
	}
	if !errors.Is(err, ErrVMNotFound) {
		t.Errorf("err = %v, want errors.Is(err, ErrVMNotFound)", err)
	}
}

// TestRenameVM_TargetExists asserts that renaming over an existing
// VM directory is refused before any rename(2) call.
func TestRenameVM_TargetExists(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeTreeVM(t, "src", vmconfig.Config{})
	writeTreeVM(t, "dst", vmconfig.Config{})

	err := RenameVM("src", "dst")
	if err == nil {
		t.Fatal("RenameVM(src, dst) returned nil; want target-exists error")
	}
	if !errors.Is(err, ErrVMRenameTargetExists) {
		t.Errorf("err = %v, want errors.Is(err, ErrVMRenameTargetExists)", err)
	}
	// Source must remain in place on refusal.
	if _, statErr := os.Stat(filepath.Join(vmconfig.BaseDir(), "src")); statErr != nil {
		t.Errorf("src directory removed despite refusal: %v", statErr)
	}
}

// TestExportImportVM_RoundTrip pins the export/import contract:
// export writes a tar.gz, import reconstructs the disk image, and
// the auto-extension logic appends .tar.gz when missing.
func TestExportImportVM_RoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeTreeVM(t, "orig", vmconfig.Config{CPU: 2, MemoryGB: 4})

	dest := filepath.Join(t.TempDir(), "archive") // no extension
	if err := ExportVM("orig", dest); err != nil {
		t.Fatalf("ExportVM error = %v", err)
	}
	wantPath := dest + ".tar.gz"
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("expected archive at %s: %v", wantPath, err)
	}

	if err := ImportVM(wantPath, "imported"); err != nil {
		t.Fatalf("ImportVM error = %v", err)
	}
	importedDisk := filepath.Join(vmconfig.BaseDir(), "imported", "linux-disk.img")
	data, err := os.ReadFile(importedDisk)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", importedDisk, err)
	}
	if string(data) != "disk" {
		t.Errorf("imported disk content = %q, want %q", string(data), "disk")
	}
}

// TestImportVM_RefusesExistingTarget asserts the pre-flight check:
// importing into an existing VM name fails before opening the archive.
func TestImportVM_RefusesExistingTarget(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeTreeVM(t, "src", vmconfig.Config{})
	writeTreeVM(t, "occupied", vmconfig.Config{})

	archive := filepath.Join(t.TempDir(), "src.tar.gz")
	if err := ExportVM("src", archive); err != nil {
		t.Fatalf("ExportVM error = %v", err)
	}

	err := ImportVM(archive, "occupied")
	if err == nil {
		t.Fatal("ImportVM into existing VM returned nil; want refusal")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("err = %q, want substring 'already exists'", err.Error())
	}
}

// TestImportVM_ArchiveNotFound asserts that a missing archive
// path produces a clear error rather than a generic open failure.
func TestImportVM_ArchiveNotFound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	err := ImportVM("/definitely/does/not/exist.tar.gz", "newvm")
	if err == nil {
		t.Fatal("ImportVM(missing) returned nil; want not-found error")
	}
	if !strings.Contains(err.Error(), "archive not found") {
		t.Errorf("err = %q, want substring 'archive not found'", err.Error())
	}
}
