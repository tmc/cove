package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreallocateFileZeroSizeIsNoop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "disk.img")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	defer f.Close()
	if err := preallocateFile(f, 0); err != nil {
		t.Fatalf("preallocateFile(0): %v", err)
	}
	info, err := f.Stat()
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("size = %d, want 0", info.Size())
	}
}

func TestCheckDiskSpaceZeroBytesAlwaysOK(t *testing.T) {
	if err := checkDiskSpace(t.TempDir(), 0); err != nil {
		t.Fatalf("checkDiskSpace(_, 0) = %v, want nil", err)
	}
	if err := checkDiskSpace("/nonexistent", -1); err != nil {
		t.Fatalf("checkDiskSpace(bad, -1) = %v, want nil for non-positive need", err)
	}
}

func TestCheckDiskSpaceMissingDirErrors(t *testing.T) {
	err := checkDiskSpace("/nonexistent/does/not/exist", 1)
	if err == nil || !strings.Contains(err.Error(), "statfs") {
		t.Fatalf("err = %v, want 'statfs' error", err)
	}
}
