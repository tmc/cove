package ociimage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
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

// WriteCompressedChunkAt verifies, decompresses, and writes one compressed disk chunk.
func WriteCompressedChunkAt(w io.WriterAt, c Chunk, desc Descriptor, compressed io.Reader) error {
	if c.Size < 0 {
		return fmt.Errorf("write compressed chunk %d: negative size %d", c.Index, c.Size)
	}
	if c.Digest == "" {
		return fmt.Errorf("write compressed chunk %d: missing digest", c.Index)
	}
	if desc.Digest == "" {
		return fmt.Errorf("write compressed chunk %d: missing compressed digest", c.Index)
	}
	if desc.Size < 0 {
		return fmt.Errorf("write compressed chunk %d: negative compressed size %d", c.Index, desc.Size)
	}
	if desc.MediaType != "" && desc.MediaType != MediaTypeLayerLZ4 {
		return fmt.Errorf("write compressed chunk %d: media type %q, want %q", c.Index, desc.MediaType, MediaTypeLayerLZ4)
	}

	cr := &digestReader{r: compressed, h: sha256.New()}
	cw := &chunkStreamWriter{w: w, chunk: c, h: sha256.New()}
	if _, err := io.Copy(cw, lz4.NewReader(cr)); err != nil {
		return fmt.Errorf("write compressed chunk %d: %w", c.Index, err)
	}
	if _, err := io.Copy(io.Discard, cr); err != nil {
		return fmt.Errorf("write compressed chunk %d: read compressed data: %w", c.Index, err)
	}
	if cr.n != desc.Size {
		return fmt.Errorf("write compressed chunk %d: compressed size %d, want %d", c.Index, cr.n, desc.Size)
	}
	if got := digestSum(cr.h); got != desc.Digest {
		return fmt.Errorf("write compressed chunk %d: compressed digest %s, want %s", c.Index, got, desc.Digest)
	}
	if cw.n != c.Size {
		return fmt.Errorf("write compressed chunk %d: size %d, want %d", c.Index, cw.n, c.Size)
	}
	if got := digestSum(cw.h); got != c.Digest {
		return fmt.Errorf("write compressed chunk %d: digest %s, want %s", c.Index, got, c.Digest)
	}
	return nil
}

type digestReader struct {
	r io.Reader
	h hash.Hash
	n int64
}

func (r *digestReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		_, _ = r.h.Write(p[:n])
		r.n += int64(n)
	}
	return n, err
}

type chunkStreamWriter struct {
	w     io.WriterAt
	chunk Chunk
	h     hash.Hash
	n     int64
}

func (w *chunkStreamWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if int64(len(p)) > w.chunk.Size-w.n {
		return 0, fmt.Errorf("write chunk %d: uncompressed size exceeds %d", w.chunk.Index, w.chunk.Size)
	}
	if w.chunk.Zero {
		if !allZero(p) {
			return 0, fmt.Errorf("write chunk %d: non-zero data for zero chunk", w.chunk.Index)
		}
	} else {
		n, err := w.w.WriteAt(p, w.chunk.Offset+w.n)
		if err != nil {
			return n, fmt.Errorf("write chunk %d: %w", w.chunk.Index, err)
		}
		if n != len(p) {
			return n, fmt.Errorf("write chunk %d: %w", w.chunk.Index, io.ErrShortWrite)
		}
	}
	_, _ = w.h.Write(p)
	w.n += int64(len(p))
	return len(p), nil
}

func digestSum(h hash.Hash) string {
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
