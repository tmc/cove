package ociimage

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
)

const (
	// DefaultChunkSize is the v0.1 fixed disk chunk size.
	DefaultChunkSize int64 = 512 << 20
)

// Chunk describes one uncompressed disk chunk.
type Chunk struct {
	Index  int
	Offset int64
	Size   int64
	Digest string
	Zero   bool
}

// DescribeChunks reads r and returns fixed-size uncompressed chunk metadata.
func DescribeChunks(r io.Reader, chunkSize int64) ([]Chunk, error) {
	if chunkSize <= 0 {
		return nil, fmt.Errorf("invalid chunk size %d", chunkSize)
	}

	bufSize := int64(1 << 20)
	if chunkSize < bufSize {
		bufSize = chunkSize
	}
	buf := make([]byte, int(bufSize))

	var chunks []Chunk
	for index, offset := 0, int64(0); ; index++ {
		chunk, err := describeChunk(r, index, offset, chunkSize, buf)
		if err == io.EOF {
			return chunks, nil
		}
		if err != nil {
			return nil, err
		}
		chunks = append(chunks, chunk)
		offset += chunk.Size
	}
}

// ChunkLayerAnnotations returns cove annotations for a disk chunk layer.
func ChunkLayerAnnotations(c Chunk, total int) map[string]string {
	out := map[string]string{
		CoveRole:                      "disk",
		CoveUncompressedSize:          fmt.Sprint(c.Size),
		CoveUncompressedContentDigest: c.Digest,
		CoveChunkIndex:                fmt.Sprint(c.Index),
		CoveChunkTotal:                fmt.Sprint(total),
	}
	if c.Zero {
		out[CoveZeroChunk] = "true"
	}
	return out
}

func describeChunk(r io.Reader, index int, offset, chunkSize int64, buf []byte) (Chunk, error) {
	h := sha256.New()
	zero := true
	var size int64

	for size < chunkSize {
		n, err := readChunkPart(r, buf, chunkSize-size)
		if n > 0 {
			b := buf[:n]
			_, _ = h.Write(b)
			if zero && !allZero(b) {
				zero = false
			}
			size += int64(n)
		}
		if err == io.EOF {
			if size == 0 {
				return Chunk{}, io.EOF
			}
			return newChunk(index, offset, size, h.Sum(nil), zero), nil
		}
		if err != nil {
			return Chunk{}, fmt.Errorf("read chunk %d: %w", index, err)
		}
		if n == 0 {
			return Chunk{}, fmt.Errorf("read chunk %d: no progress", index)
		}
	}
	return newChunk(index, offset, size, h.Sum(nil), zero), nil
}

func readChunkPart(r io.Reader, buf []byte, remaining int64) (int, error) {
	n := int64(len(buf))
	if remaining < n {
		n = remaining
	}
	return r.Read(buf[:int(n)])
}

func newChunk(index int, offset, size int64, digest []byte, zero bool) Chunk {
	return Chunk{
		Index:  index,
		Offset: offset,
		Size:   size,
		Digest: "sha256:" + hex.EncodeToString(digest),
		Zero:   zero,
	}
}

func allZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}
