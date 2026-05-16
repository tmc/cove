package ociimage

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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

func digestBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
