// applelz4.go — pure-Go encoder/decoder for Apple's "LZ4 framed" stream
// format. This is the wire format produced by Apple's Compression framework
// (libcompression.dylib) when invoked with COMPRESSION_LZ4 — it is *not*
// the LZ4 frame format defined by the LZ4 spec (magic 0x184D2204).
//
// We need it because cirruslabs/tart compresses VM disk layers with
// `(data as NSData).compressed(using: .lz4)`, which writes this framing.
// Pulling a tart image therefore requires decoding it; pushing in
// tart-compatible format requires producing it.
//
// Stream shape (all sizes are little-endian uint32):
//
//	┌───────────┬─────────┬─────────┬───────────────────┐
//	│ "bv41"    │ raw_sz  │ comp_sz │ LZ4 raw block ... │   compressed block
//	└───────────┴─────────┴─────────┴───────────────────┘
//	┌───────────┬─────────┬───────────────┐
//	│ "bv4-"    │ raw_sz  │ raw bytes ... │                  uncompressed block
//	└───────────┴─────────┴───────────────┘
//	┌───────────┐
//	│ "bv4$"    │                                            end of stream
//	└───────────┘
//
// The encoder packages each input chunk as one compressed block (or
// uncompressed if compression would inflate), followed by an end-of-stream
// marker. The decoder iterates blocks until it sees end-of-stream.
//
// References:
//   - Apple's <Compression/Compression.h>: COMPRESSION_LZ4 documentation
//     describes the magic header and end marker.
//   - libcompression source (closed) — the framing has been stable since
//     macOS 10.11.
//   - Reverse-engineered specs in the public domain document the magic
//     bytes and field layout used here.
//
// LZ4 block (de)compression is delegated to github.com/pierrec/lz4/v4's
// UncompressBlock / CompressBlock — they implement the LZ4 raw block spec
// that Apple wraps.

package ociimage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/pierrec/lz4/v4"
)

// Apple LZ4 framing magic bytes. These appear at the start of each block.
var (
	appleLZ4MagicCompressed   = [4]byte{'b', 'v', '4', '1'}
	appleLZ4MagicUncompressed = [4]byte{'b', 'v', '4', '-'}
	appleLZ4MagicEndOfStream  = [4]byte{'b', 'v', '4', '$'}
)

// appleLZ4MaxBlockSize caps any single input block written by the encoder.
// Apple's libcompression writes blocks of at most 64 MiB; mirror that to
// keep our streams interoperable with strict decoders.
const appleLZ4MaxBlockSize = 64 * 1024 * 1024

// CompressAppleLZ4 returns src encoded as an Apple-LZ4 stream.
//
// Each call emits one or more compressed blocks (one per appleLZ4MaxBlockSize
// chunk of input) followed by an end-of-stream marker. If LZ4 would inflate
// a block (compressed bound exceeds raw size, e.g. random data), the block
// is emitted uncompressed instead — this matches libcompression's behaviour
// and keeps decoders that strictly check sizes happy.
func CompressAppleLZ4(src []byte) ([]byte, error) {
	var out bytes.Buffer
	if err := writeAppleLZ4(&out, src); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// DecompressAppleLZ4 returns the decoded bytes from an Apple-LZ4 stream.
// A truncated stream (no end-of-stream marker) returns ErrUnexpectedEOF.
func DecompressAppleLZ4(src []byte) ([]byte, error) {
	var out bytes.Buffer
	if err := readAppleLZ4(bytes.NewReader(src), &out); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// writeAppleLZ4 emits src as a sequence of bv41/bv4- blocks followed by bv4$.
func writeAppleLZ4(w io.Writer, src []byte) error {
	for len(src) > 0 {
		n := len(src)
		if n > appleLZ4MaxBlockSize {
			n = appleLZ4MaxBlockSize
		}
		block := src[:n]
		src = src[n:]

		// Try to compress. If the compressed size would equal or exceed the
		// raw size — rare but possible for small/incompressible blocks — emit
		// the block uncompressed instead.
		compBound := lz4.CompressBlockBound(len(block))
		compBuf := make([]byte, compBound)
		compSize, err := lz4.CompressBlock(block, compBuf, nil)
		if err != nil {
			return fmt.Errorf("apple lz4 compress block: %w", err)
		}
		if compSize == 0 || compSize >= len(block) {
			if err := writeAppleLZ4Uncompressed(w, block); err != nil {
				return err
			}
			continue
		}
		if err := writeAppleLZ4Compressed(w, block, compBuf[:compSize]); err != nil {
			return err
		}
	}
	if _, err := w.Write(appleLZ4MagicEndOfStream[:]); err != nil {
		return fmt.Errorf("apple lz4 write eos: %w", err)
	}
	return nil
}

func writeAppleLZ4Compressed(w io.Writer, raw, comp []byte) error {
	var hdr [12]byte
	copy(hdr[0:4], appleLZ4MagicCompressed[:])
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(len(raw)))
	binary.LittleEndian.PutUint32(hdr[8:12], uint32(len(comp)))
	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("apple lz4 write compressed header: %w", err)
	}
	if _, err := w.Write(comp); err != nil {
		return fmt.Errorf("apple lz4 write compressed payload: %w", err)
	}
	return nil
}

func writeAppleLZ4Uncompressed(w io.Writer, raw []byte) error {
	var hdr [8]byte
	copy(hdr[0:4], appleLZ4MagicUncompressed[:])
	binary.LittleEndian.PutUint32(hdr[4:8], uint32(len(raw)))
	if _, err := w.Write(hdr[:]); err != nil {
		return fmt.Errorf("apple lz4 write uncompressed header: %w", err)
	}
	if _, err := w.Write(raw); err != nil {
		return fmt.Errorf("apple lz4 write uncompressed payload: %w", err)
	}
	return nil
}

// uncompressLZ4BlockWithDict decodes one raw LZ4 block whose back-references
// may extend into dict (the previous block's decoded output, capped at 64
// KiB). Apple-LZ4 streams are dependent: each compressed block uses its
// predecessor as a dictionary. Wraps pierrec/lz4's UncompressBlockWithDict.
func uncompressLZ4BlockWithDict(src, dst, dict []byte) (int, error) {
	return lz4.UncompressBlockWithDict(src, dst, dict)
}

// readAppleLZ4 decodes an Apple-LZ4 stream from r into w. It iterates blocks
// until an end-of-stream marker. Any other 4-byte prefix is an error.
//
// Apple's LZ4 framing uses *dependent* blocks: each block may reference back
// into the previous block's output (up to a 64 KiB window) as a dictionary.
// We therefore retain the decoded bytes of the most recent block and pass
// them to UncompressBlockWithDict for the next one. The first block decodes
// with no dictionary.
func readAppleLZ4(r io.Reader, w io.Writer) error {
	br := bufferedByteReader(r)
	var dict []byte
	for {
		var magic [4]byte
		if _, err := io.ReadFull(br, magic[:]); err != nil {
			if errors.Is(err, io.EOF) {
				return io.ErrUnexpectedEOF
			}
			return fmt.Errorf("apple lz4 read magic: %w", err)
		}
		switch magic {
		case appleLZ4MagicEndOfStream:
			return nil
		case appleLZ4MagicCompressed:
			out, err := readAppleLZ4Compressed(br, w, dict)
			if err != nil {
				return err
			}
			dict = trimAppleLZ4Dict(out)
		case appleLZ4MagicUncompressed:
			out, err := readAppleLZ4Uncompressed(br, w)
			if err != nil {
				return err
			}
			dict = trimAppleLZ4Dict(out)
		default:
			return fmt.Errorf("apple lz4 unknown block magic %q", magic[:])
		}
	}
}

func readAppleLZ4Compressed(r io.Reader, w io.Writer, dict []byte) ([]byte, error) {
	var sizes [8]byte
	if _, err := io.ReadFull(r, sizes[:]); err != nil {
		return nil, fmt.Errorf("apple lz4 read compressed sizes: %w", err)
	}
	rawSize := binary.LittleEndian.Uint32(sizes[0:4])
	compSize := binary.LittleEndian.Uint32(sizes[4:8])
	if rawSize > appleLZ4MaxBlockSize || compSize > appleLZ4MaxBlockSize {
		return nil, fmt.Errorf("apple lz4 compressed block sizes raw=%d comp=%d exceed cap %d", rawSize, compSize, appleLZ4MaxBlockSize)
	}
	comp := make([]byte, compSize)
	if _, err := io.ReadFull(r, comp); err != nil {
		return nil, fmt.Errorf("apple lz4 read compressed payload: %w", err)
	}
	dst := make([]byte, rawSize)
	n, err := lz4.UncompressBlockWithDict(comp, dst, dict)
	if err != nil {
		return nil, fmt.Errorf("apple lz4 uncompress block: %w", err)
	}
	if uint32(n) != rawSize {
		return nil, fmt.Errorf("apple lz4 uncompress block: produced %d bytes, want %d", n, rawSize)
	}
	if _, err := w.Write(dst); err != nil {
		return nil, fmt.Errorf("apple lz4 write decoded: %w", err)
	}
	return dst, nil
}

// trimAppleLZ4Dict caps the back-reference window we retain to the last 64
// KiB of decoded output, matching the LZ4 maximum offset.
func trimAppleLZ4Dict(prev []byte) []byte {
	const window = 64 * 1024
	if len(prev) <= window {
		return prev
	}
	return prev[len(prev)-window:]
}

func readAppleLZ4Uncompressed(r io.Reader, w io.Writer) ([]byte, error) {
	var sizeBuf [4]byte
	if _, err := io.ReadFull(r, sizeBuf[:]); err != nil {
		return nil, fmt.Errorf("apple lz4 read uncompressed size: %w", err)
	}
	rawSize := binary.LittleEndian.Uint32(sizeBuf[:])
	if rawSize > appleLZ4MaxBlockSize {
		return nil, fmt.Errorf("apple lz4 uncompressed block size %d exceeds cap %d", rawSize, appleLZ4MaxBlockSize)
	}
	payload := make([]byte, rawSize)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, fmt.Errorf("apple lz4 read uncompressed payload: %w", err)
	}
	if _, err := w.Write(payload); err != nil {
		return nil, fmt.Errorf("apple lz4 write uncompressed payload: %w", err)
	}
	return payload, nil
}

// bufferedByteReader returns r as an io.Reader that doesn't pessimize small
// reads. Apple-LZ4 framing reads in 4- and 8-byte chunks; bare net.Conn or
// http.Body would do a syscall per read otherwise.
func bufferedByteReader(r io.Reader) io.Reader {
	if _, ok := r.(*bytes.Reader); ok {
		return r
	}
	if _, ok := r.(*bytes.Buffer); ok {
		return r
	}
	type bufReader interface {
		io.Reader
		Buffered() int
	}
	if _, ok := r.(bufReader); ok {
		return r
	}
	return &smallReadBuffer{r: r}
}

// smallReadBuffer is a minimal buffer for io.Readers that don't implement
// any kind of buffering. We don't import bufio because that would add a
// 4096-byte allocation per stream when most callers feed us bytes.Reader
// already.
type smallReadBuffer struct {
	r   io.Reader
	buf [16]byte
	n   int
	off int
}

func (b *smallReadBuffer) Read(p []byte) (int, error) {
	if b.off < b.n {
		n := copy(p, b.buf[b.off:b.n])
		b.off += n
		return n, nil
	}
	if len(p) >= len(b.buf) {
		return b.r.Read(p)
	}
	n, err := b.r.Read(b.buf[:])
	if n > 0 {
		b.n = n
		b.off = 0
		nn := copy(p, b.buf[b.off:b.n])
		b.off += nn
		return nn, nil
	}
	return 0, err
}
