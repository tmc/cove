package ociimage

import (
	"errors"
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

func TestWriteChunkAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "disk.img.partial")
	f, err := CreatePartialDisk(path, 8)
	if err != nil {
		t.Fatalf("CreatePartialDisk(): %v", err)
	}
	defer f.Close()

	data := []byte("abc")
	chunk := Chunk{
		Index:  1,
		Offset: 2,
		Size:   int64(len(data)),
		Digest: testDigest(data),
	}
	if err := WriteChunkAt(f, chunk, data); err != nil {
		t.Fatalf("WriteChunkAt(): %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(): %v", err)
	}
	want := []byte{0, 0, 'a', 'b', 'c', 0, 0, 0}
	if string(got) != string(want) {
		t.Fatalf("disk bytes = %v, want %v", got, want)
	}
}

func TestWriteChunkAtSkipsZeroChunk(t *testing.T) {
	w := &recordingWriterAt{}
	data := []byte{0, 0, 0}
	chunk := Chunk{
		Index:  2,
		Offset: 4,
		Size:   int64(len(data)),
		Digest: testDigest(data),
		Zero:   true,
	}
	if err := WriteChunkAt(w, chunk, data); err != nil {
		t.Fatalf("WriteChunkAt(): %v", err)
	}
	if w.called {
		t.Fatal("WriteChunkAt() wrote zero chunk, want sparse skip")
	}
}

func TestWriteChunkAtRejectsBadInput(t *testing.T) {
	tests := []struct {
		name    string
		chunk   Chunk
		data    []byte
		wantErr string
	}{
		{
			name:    "missing digest",
			chunk:   Chunk{Index: 1, Size: 1},
			data:    []byte{1},
			wantErr: "missing digest",
		},
		{
			name:    "size mismatch",
			chunk:   Chunk{Index: 1, Size: 2, Digest: testDigest([]byte{1})},
			data:    []byte{1},
			wantErr: "data size",
		},
		{
			name:    "digest mismatch",
			chunk:   Chunk{Index: 1, Size: 1, Digest: testDigest([]byte{2})},
			data:    []byte{1},
			wantErr: "digest",
		},
		{
			name:    "zero flag mismatch",
			chunk:   Chunk{Index: 1, Size: 1, Digest: testDigest([]byte{1}), Zero: true},
			data:    []byte{1},
			wantErr: "non-zero data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := WriteChunkAt(&recordingWriterAt{}, tt.chunk, tt.data)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("WriteChunkAt() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestWriteChunkAtWriteError(t *testing.T) {
	data := []byte("abc")
	errWrite := errors.New("boom")
	w := &recordingWriterAt{err: errWrite}
	chunk := Chunk{Index: 1, Size: int64(len(data)), Digest: testDigest(data)}
	err := WriteChunkAt(w, chunk, data)
	if !errors.Is(err, errWrite) {
		t.Fatalf("WriteChunkAt() error = %v, want %v", err, errWrite)
	}
}

type recordingWriterAt struct {
	called bool
	err    error
}

func (w *recordingWriterAt) WriteAt([]byte, int64) (int, error) {
	w.called = true
	if w.err != nil {
		return 0, w.err
	}
	return 0, io.ErrShortWrite
}
