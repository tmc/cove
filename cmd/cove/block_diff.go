package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
)

const buildDeltaBlockSize = 64 << 10

type diskDelta struct {
	BlockSize int64            `json:"block_size"`
	Size      int64            `json:"size"`
	Blocks    []diskDeltaBlock `json:"blocks"`
}

type diskDeltaBlock struct {
	Offset int64  `json:"offset"`
	Data   []byte `json:"data"`
}

func DiffDisks(parentPath, childPath string) (*diskDelta, error) {
	parent, err := os.Open(parentPath)
	if err != nil {
		return nil, fmt.Errorf("diff parent: %w", err)
	}
	defer parent.Close()
	child, err := os.Open(childPath)
	if err != nil {
		return nil, fmt.Errorf("diff child: %w", err)
	}
	defer child.Close()
	info, err := child.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat child: %w", err)
	}
	return diffDiskReaders(parent, child, info.Size(), buildDeltaBlockSize)
}

func diffDiskReaders(parent, child io.ReaderAt, childSize, blockSize int64) (*diskDelta, error) {
	if blockSize <= 0 {
		return nil, fmt.Errorf("diff disks: invalid block size %d", blockSize)
	}
	if childSize < 0 {
		return nil, fmt.Errorf("diff disks: invalid child size %d", childSize)
	}
	delta := &diskDelta{BlockSize: blockSize, Size: childSize}
	parentBuf := make([]byte, blockSize)
	childBuf := make([]byte, blockSize)
	for off := int64(0); off < childSize; off += blockSize {
		n := blockSize
		if remain := childSize - off; remain < n {
			n = remain
		}
		clear(parentBuf)
		clear(childBuf)
		if err := readAtFullOrZero(parent, parentBuf[:n], off); err != nil {
			return nil, fmt.Errorf("read parent at %d: %w", off, err)
		}
		if err := readAtFullOrZero(child, childBuf[:n], off); err != nil {
			return nil, fmt.Errorf("read child at %d: %w", off, err)
		}
		if bytes.Equal(parentBuf[:n], childBuf[:n]) {
			continue
		}
		data := append([]byte(nil), childBuf[:n]...)
		delta.Blocks = append(delta.Blocks, diskDeltaBlock{Offset: off, Data: data})
	}
	return delta, nil
}

func readAtFullOrZero(r io.ReaderAt, p []byte, off int64) error {
	n, err := r.ReadAt(p, off)
	if err == nil {
		return nil
	}
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		clear(p[n:])
		return nil
	}
	return err
}

func ApplyDiskDelta(parentPath, childPath string, delta *diskDelta) error {
	if delta == nil {
		return fmt.Errorf("apply delta: nil delta")
	}
	if delta.BlockSize <= 0 {
		return fmt.Errorf("apply delta: invalid block size %d", delta.BlockSize)
	}
	if delta.Size < 0 {
		return fmt.Errorf("apply delta: invalid size %d", delta.Size)
	}
	partialPath := childPath + ".partial"
	if err := os.Remove(partialPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("apply delta: remove partial: %w", err)
	}
	if err := cloneFile(parentPath, partialPath); err != nil {
		if err := copyFile(parentPath, partialPath); err != nil {
			return fmt.Errorf("apply delta: copy parent: %w", err)
		}
	}
	f, err := os.OpenFile(partialPath, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("apply delta: open child: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			f.Close()
		}
	}()
	if err := f.Truncate(delta.Size); err != nil {
		return fmt.Errorf("apply delta: truncate: %w", err)
	}
	for _, b := range delta.Blocks {
		if b.Offset < 0 || b.Offset%delta.BlockSize != 0 {
			return fmt.Errorf("apply delta: invalid offset %d", b.Offset)
		}
		if int64(len(b.Data)) > delta.BlockSize || b.Offset+int64(len(b.Data)) > delta.Size {
			return fmt.Errorf("apply delta: invalid block at %d", b.Offset)
		}
		n, err := f.WriteAt(b.Data, b.Offset)
		if err != nil {
			return fmt.Errorf("apply delta: write at %d: %w", b.Offset, err)
		}
		if n != len(b.Data) {
			return fmt.Errorf("apply delta: write at %d: %w", b.Offset, io.ErrShortWrite)
		}
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("apply delta: sync: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("apply delta: close: %w", err)
	}
	closed = true
	if err := os.Rename(partialPath, childPath); err != nil {
		return fmt.Errorf("apply delta: rename partial: %w", err)
	}
	return nil
}
