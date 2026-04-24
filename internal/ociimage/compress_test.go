package ociimage

import (
	"bytes"
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
