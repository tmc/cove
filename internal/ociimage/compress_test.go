package ociimage

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareChunkLayerCompressesAndVerifies(t *testing.T) {
	data := []byte{1, 2, 3, 4}
	chunks, err := DescribeChunks(bytes.NewReader(data), 4)
	if err != nil {
		t.Fatalf("DescribeChunks(): %v", err)
	}

	got, err := PrepareChunkLayer(bytes.NewReader(data), chunks[0], 1, true)
	if err != nil {
		t.Fatalf("PrepareChunkLayer(): %v", err)
	}
	if got.SkipUpload {
		t.Fatal("SkipUpload = true, want false")
	}
	if got.Descriptor.MediaType != MediaTypeLayerLZ4 {
		t.Fatalf("MediaType = %q, want %q", got.Descriptor.MediaType, MediaTypeLayerLZ4)
	}
	if got.Descriptor.Size != int64(len(got.Data)) {
		t.Fatalf("Descriptor.Size = %d, want %d", got.Descriptor.Size, len(got.Data))
	}
	if got.Descriptor.Digest != testDigest(got.Data) {
		t.Fatalf("Descriptor.Digest = %q, want %q", got.Descriptor.Digest, testDigest(got.Data))
	}
	if got.Descriptor.Annotations[CoveUncompressedContentDigest] != chunks[0].Digest {
		t.Fatalf("chunk digest annotation = %q", got.Descriptor.Annotations[CoveUncompressedContentDigest])
	}
	if got.Descriptor.Annotations[LumeUncompressedContentDigest] != chunks[0].Digest {
		t.Fatalf("lume chunk digest annotation = %q", got.Descriptor.Annotations[LumeUncompressedContentDigest])
	}

	plain, err := DecompressChunkData(chunks[0], got.Data)
	if err != nil {
		t.Fatalf("DecompressChunkData(): %v", err)
	}
	if !bytes.Equal(plain, data) {
		t.Fatalf("decompressed = %v, want %v", plain, data)
	}
}

func TestPrepareChunkLayerSkipsZeroChunk(t *testing.T) {
	data := []byte{0, 0, 0, 0}
	chunks, err := DescribeChunks(bytes.NewReader(data), 4)
	if err != nil {
		t.Fatalf("DescribeChunks(): %v", err)
	}

	got, err := PrepareChunkLayer(bytes.NewReader(data), chunks[0], 1, false)
	if err != nil {
		t.Fatalf("PrepareChunkLayer(): %v", err)
	}
	if !got.SkipUpload {
		t.Fatal("SkipUpload = false, want true")
	}
	if len(got.Data) != 0 {
		t.Fatalf("Data length = %d, want 0", len(got.Data))
	}
	if got.Descriptor.MediaType != "" {
		t.Fatalf("Descriptor = %#v, want zero value", got.Descriptor)
	}
}

func TestReadChunkAtRejectsDigestMismatch(t *testing.T) {
	chunk := Chunk{Index: 0, Offset: 0, Size: 3, Digest: testDigest([]byte("bad"))}
	_, err := ReadChunkAt(bytes.NewReader([]byte{1, 2, 3}), chunk)
	if err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("ReadChunkAt() error = %v, want digest mismatch", err)
	}
}

func TestReadChunkAtRejectsShortRead(t *testing.T) {
	chunk := Chunk{Index: 0, Offset: 0, Size: 4, Digest: testDigest([]byte{1, 2, 3, 0})}
	_, err := ReadChunkAt(bytes.NewReader([]byte{1, 2, 3}), chunk)
	if err == nil || !strings.Contains(err.Error(), "EOF") {
		t.Fatalf("ReadChunkAt() error = %v, want EOF", err)
	}
}

func TestDecompressChunkDataRejectsDigestMismatch(t *testing.T) {
	data := []byte{1, 2, 3}
	compressed, err := CompressChunkData(data)
	if err != nil {
		t.Fatalf("CompressChunkData(): %v", err)
	}
	chunk := Chunk{Index: 0, Size: 3, Digest: testDigest([]byte("bad"))}
	_, err = DecompressChunkData(chunk, compressed)
	if err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("DecompressChunkData() error = %v, want digest mismatch", err)
	}
}

func TestWriteCompressedChunkAt(t *testing.T) {
	data := []byte("abc")
	compressed, desc := compressedTestLayer(t, data)
	chunk := Chunk{
		Index:  1,
		Offset: 2,
		Size:   int64(len(data)),
		Digest: testDigest(data),
	}
	path := filepath.Join(t.TempDir(), "disk.img.partial")
	f, err := CreatePartialDisk(path, 8)
	if err != nil {
		t.Fatalf("CreatePartialDisk(): %v", err)
	}
	defer f.Close()

	if err := WriteCompressedChunkAt(f, chunk, desc, bytes.NewReader(compressed)); err != nil {
		t.Fatalf("WriteCompressedChunkAt(): %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(): %v", err)
	}
	want := []byte{0, 0, 'a', 'b', 'c', 0, 0, 0}
	if !bytes.Equal(got, want) {
		t.Fatalf("disk bytes = %v, want %v", got, want)
	}
}

func TestWriteCompressedChunkAtRejectsBadInput(t *testing.T) {
	data := []byte("abc")
	compressed, desc := compressedTestLayer(t, data)
	chunk := Chunk{Index: 1, Size: int64(len(data)), Digest: testDigest(data)}

	tests := []struct {
		name    string
		chunk   Chunk
		desc    Descriptor
		writer  io.WriterAt
		wantErr string
	}{
		{
			name:    "negative size",
			chunk:   Chunk{Index: 1, Size: -1, Digest: testDigest(data)},
			desc:    desc,
			writer:  tempDiskWriter(t, int64(len(data))),
			wantErr: "negative size",
		},
		{
			name:    "missing digest",
			chunk:   Chunk{Index: 1, Size: int64(len(data))},
			desc:    desc,
			writer:  tempDiskWriter(t, int64(len(data))),
			wantErr: "missing digest",
		},
		{
			name:    "missing compressed digest",
			chunk:   chunk,
			desc:    Descriptor{Size: desc.Size},
			writer:  tempDiskWriter(t, int64(len(data))),
			wantErr: "missing compressed digest",
		},
		{
			name:    "negative compressed size",
			chunk:   chunk,
			desc:    descriptorWithSize(desc, -1),
			writer:  tempDiskWriter(t, int64(len(data))),
			wantErr: "negative compressed size",
		},
		{
			name:    "compressed digest",
			chunk:   chunk,
			desc:    descriptorWithDigest(desc, testDigest([]byte("bad"))),
			writer:  tempDiskWriter(t, int64(len(data))),
			wantErr: "compressed digest",
		},
		{
			name:    "compressed size",
			chunk:   chunk,
			desc:    descriptorWithSize(desc, desc.Size+1),
			writer:  tempDiskWriter(t, int64(len(data))),
			wantErr: "compressed size",
		},
		{
			name:    "uncompressed digest",
			chunk:   Chunk{Index: 1, Size: int64(len(data)), Digest: testDigest([]byte("bad"))},
			desc:    desc,
			writer:  tempDiskWriter(t, int64(len(data))),
			wantErr: "digest",
		},
		{
			name:    "uncompressed size",
			chunk:   Chunk{Index: 1, Size: int64(len(data)) + 1, Digest: testDigest(data)},
			desc:    desc,
			writer:  tempDiskWriter(t, int64(len(data))+1),
			wantErr: "size",
		},
		{
			name:    "short write",
			chunk:   chunk,
			desc:    desc,
			writer:  &recordingWriterAt{},
			wantErr: "short write",
		},
		{
			name:    "media type",
			chunk:   chunk,
			desc:    descriptorWithMediaType(desc, MediaTypeLayer),
			writer:  tempDiskWriter(t, int64(len(data))),
			wantErr: "media type",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := WriteCompressedChunkAt(tt.writer, tt.chunk, tt.desc, bytes.NewReader(compressed))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("WriteCompressedChunkAt() error = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func compressedTestLayer(t *testing.T, data []byte) ([]byte, Descriptor) {
	t.Helper()

	compressed, err := CompressChunkData(data)
	if err != nil {
		t.Fatalf("CompressChunkData(): %v", err)
	}
	return compressed, Descriptor{
		MediaType: MediaTypeLayerLZ4,
		Size:      int64(len(compressed)),
		Digest:    testDigest(compressed),
	}
}

func tempDiskWriter(t *testing.T, size int64) io.WriterAt {
	t.Helper()

	f, err := CreatePartialDisk(filepath.Join(t.TempDir(), "disk.img.partial"), size)
	if err != nil {
		t.Fatalf("CreatePartialDisk(): %v", err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}

func descriptorWithDigest(d Descriptor, digest string) Descriptor {
	d.Digest = digest
	return d
}

func descriptorWithSize(d Descriptor, size int64) Descriptor {
	d.Size = size
	return d
}

func descriptorWithMediaType(d Descriptor, mediaType string) Descriptor {
	d.MediaType = mediaType
	return d
}
