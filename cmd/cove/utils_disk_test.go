package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestCreateRawDiskImageBytesPreallocates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "disk.img")
	const size = 16 * 1024 * 1024
	if err := createRawDiskImageBytes(path, size); err != nil {
		t.Fatalf("createRawDiskImageBytes: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size() != size {
		t.Fatalf("size = %d, want %d", info.Size(), size)
	}
	blocks := allocatedBlocks(t, path)
	if blocks == 0 {
		t.Fatalf("allocated blocks = 0, want preallocated file")
	}
}

func TestCreateSparseDiskImageBytesLeavesSparseFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "disk.img")
	const size = 16 * 1024 * 1024
	if err := createSparseDiskImageBytes(path, size); err != nil {
		t.Fatalf("createSparseDiskImageBytes: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size() != size {
		t.Fatalf("size = %d, want %d", info.Size(), size)
	}
	if blocks := allocatedBlocks(t, path); blocks >= size/512 {
		t.Fatalf("allocated blocks = %d, want less than full allocation", blocks)
	}
}

func TestCreateDiskImageBytesExistingIsNoop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "disk.img")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := createRawDiskImageBytes(path, 1024); err != nil {
		t.Fatalf("createRawDiskImageBytes existing: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "x" {
		t.Fatalf("existing file changed to %q", data)
	}
}

func TestCreateInstallDiskImageHonorsRawDiskFlag(t *testing.T) {
	old := rawDisk
	defer func() { rawDisk = old }()

	rawDisk = true
	path := filepath.Join(t.TempDir(), "disk.img")
	if err := createInstallDiskImageBytes(path, 16*1024*1024); err != nil {
		t.Fatalf("createInstallDiskImage: %v", err)
	}
	blocks := allocatedBlocks(t, path)
	if blocks == 0 {
		t.Fatalf("allocated blocks = 0, want raw preallocation")
	}
}

func allocatedBlocks(t *testing.T, path string) int64 {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		t.Fatalf("Stat_t: %v", err)
	}
	return st.Blocks
}
