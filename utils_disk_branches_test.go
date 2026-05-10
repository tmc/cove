package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreallocateFileRejectsNegativeSize(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "neg")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer f.Close()
	err = preallocateFile(f, -1)
	if err == nil {
		t.Fatal("preallocateFile(-1) returned nil, want error")
	}
	if !strings.Contains(err.Error(), "negative disk size") {
		t.Errorf("error = %q, want contains 'negative disk size'", err)
	}
}

func TestCreateSparseDiskImageBytesExistingIsNoop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "disk.img")
	if err := os.WriteFile(path, []byte("preexisting"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := createSparseDiskImageBytes(path, 16*1024*1024); err != nil {
		t.Fatalf("createSparseDiskImageBytes existing: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "preexisting" {
		t.Fatalf("file contents changed to %q, want preserved", data)
	}
}
