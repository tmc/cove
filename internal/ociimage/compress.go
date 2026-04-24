package ociimage

import (
	"bytes"
	"fmt"
	"io"

	"github.com/pierrec/lz4/v4"
)

const MediaTypeLayerLZ4 = "application/octet-stream+lz4"

// PreparedChunk is a disk chunk after upload preparation.
type PreparedChunk struct {
	Chunk      Chunk
	Descriptor Descriptor
	Data       []byte
	SkipUpload bool
}

// PrepareChunkLayer reads, verifies, and compresses one non-zero disk chunk.
func PrepareChunkLayer(r io.ReaderAt, c Chunk, total int, lumeCompat bool) (PreparedChunk, error) {
	var out PreparedChunk
	data, err := ReadChunkAt(r, c)
	if err != nil {
		return out, err
	}
	out.Chunk = c
	if c.Zero {
		out.SkipUpload = true
		return out, nil
	}

	compressed, err := CompressChunkData(data)
	if err != nil {
		return out, fmt.Errorf("compress chunk %d: %w", c.Index, err)
	}
	annotations := ChunkLayerAnnotations(c, total)
	if lumeCompat {
		annotations = AddLumeCompatibility(annotations)
	}
	out.Data = compressed
	out.Descriptor = Descriptor{
		MediaType:   MediaTypeLayerLZ4,
		Size:        int64(len(compressed)),
		Digest:      digestBytes(compressed),
		Annotations: annotations,
	}
	return out, nil
}

// ReadChunkAt reads and verifies one chunk from r.
func ReadChunkAt(r io.ReaderAt, c Chunk) ([]byte, error) {
	if c.Size < 0 {
		return nil, fmt.Errorf("read chunk %d: negative size %d", c.Index, c.Size)
	}
	if int64(int(c.Size)) != c.Size {
		return nil, fmt.Errorf("read chunk %d: size too large %d", c.Index, c.Size)
	}
	data := make([]byte, int(c.Size))
	n, err := r.ReadAt(data, c.Offset)
	if err != nil {
		return nil, fmt.Errorf("read chunk %d: %w", c.Index, err)
	}
	if n != len(data) {
		return nil, fmt.Errorf("read chunk %d: %w", c.Index, io.ErrShortBuffer)
	}
	if got := digestBytes(data); got != c.Digest {
		return nil, fmt.Errorf("read chunk %d: digest %s, want %s", c.Index, got, c.Digest)
	}
	if c.Zero && !allZero(data) {
		return nil, fmt.Errorf("read chunk %d: non-zero data for zero chunk", c.Index)
	}
	return data, nil
}

// CompressChunkData returns an LZ4 frame for data.
func CompressChunkData(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := lz4.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		_ = w.Close()
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DecompressChunkData decompresses and verifies one LZ4 chunk blob.
func DecompressChunkData(c Chunk, data []byte) ([]byte, error) {
	out, err := io.ReadAll(lz4.NewReader(bytes.NewReader(data)))
	if err != nil {
		return nil, fmt.Errorf("decompress chunk %d: %w", c.Index, err)
	}
	if int64(len(out)) != c.Size {
		return nil, fmt.Errorf("decompress chunk %d: size %d, want %d", c.Index, len(out), c.Size)
	}
	if got := digestBytes(out); got != c.Digest {
		return nil, fmt.Errorf("decompress chunk %d: digest %s, want %s", c.Index, got, c.Digest)
	}
	return out, nil
}
