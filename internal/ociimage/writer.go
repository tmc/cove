package ociimage

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// CreatePartialDisk creates a sparse disk file sized for direct chunk writes.
func CreatePartialDisk(path string, size int64) (*os.File, error) {
	if size < 0 {
		return nil, fmt.Errorf("create partial disk: negative size %d", size)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
	if err != nil {
		return nil, fmt.Errorf("create partial disk: %w", err)
	}
	if err := f.Truncate(size); err != nil {
		f.Close()
		return nil, fmt.Errorf("size partial disk: %w", err)
	}
	return f, nil
}

// WriteChunkAt verifies and writes one uncompressed chunk at its disk offset.
func WriteChunkAt(w io.WriterAt, c Chunk, data []byte) error {
	if c.Digest == "" {
		return fmt.Errorf("write chunk %d: missing digest", c.Index)
	}
	if int64(len(data)) != c.Size {
		return fmt.Errorf("write chunk %d: data size %d, want %d", c.Index, len(data), c.Size)
	}
	if got := digestBytes(data); got != c.Digest {
		return fmt.Errorf("write chunk %d: digest %s, want %s", c.Index, got, c.Digest)
	}
	if c.Zero {
		if !allZero(data) {
			return fmt.Errorf("write chunk %d: non-zero data for zero chunk", c.Index)
		}
		return nil
	}

	n, err := w.WriteAt(data, c.Offset)
	if err != nil {
		return fmt.Errorf("write chunk %d: %w", c.Index, err)
	}
	if n != len(data) {
		return fmt.Errorf("write chunk %d: %w", c.Index, io.ErrShortWrite)
	}
	return nil
}

func digestBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
