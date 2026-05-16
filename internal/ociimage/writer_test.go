package ociimage

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreatePartialDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "disk.img.partial")
	f, err := CreatePartialDisk(path, 1024)
	if err != nil {
		t.Fatalf("CreatePartialDisk(): %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(): %v", err)
	}
	if info.Size() != 1024 {
		t.Fatalf("partial disk size = %d, want 1024", info.Size())
	}
}

func TestCreatePartialDiskRejectsNegativeSize(t *testing.T) {
	_, err := CreatePartialDisk(filepath.Join(t.TempDir(), "disk.img.partial"), -1)
	if err == nil || !strings.Contains(err.Error(), "negative size") {
		t.Fatalf("CreatePartialDisk() error = %v, want negative size", err)
	}
}

type recordingWriterAt struct{}

func (w *recordingWriterAt) WriteAt([]byte, int64) (int, error) {
	return 0, io.ErrShortWrite
}
