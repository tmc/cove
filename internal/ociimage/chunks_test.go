package ociimage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestDescribeChunks(t *testing.T) {
	in := []byte{0, 0, 0, 0, 1, 2, 3, 4, 0, 0}
	got, err := DescribeChunks(bytes.NewReader(in), 4)
	if err != nil {
		t.Fatalf("DescribeChunks(): %v", err)
	}

	want := []Chunk{
		{Index: 0, Offset: 0, Size: 4, Digest: testDigest(in[:4]), Zero: true},
		{Index: 1, Offset: 4, Size: 4, Digest: testDigest(in[4:8]), Zero: false},
		{Index: 2, Offset: 8, Size: 2, Digest: testDigest(in[8:]), Zero: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DescribeChunks() = %#v, want %#v", got, want)
	}
}

func TestDescribeChunksEmpty(t *testing.T) {
	got, err := DescribeChunks(bytes.NewReader(nil), 4)
	if err != nil {
		t.Fatalf("DescribeChunks(): %v", err)
	}
	if got != nil {
		t.Fatalf("DescribeChunks() = %#v, want nil", got)
	}
}

func TestDescribeChunksInvalidSize(t *testing.T) {
	if _, err := DescribeChunks(bytes.NewReader(nil), 0); err == nil || !strings.Contains(err.Error(), "invalid chunk size") {
		t.Fatalf("DescribeChunks() error = %v, want invalid chunk size", err)
	}
}

func TestDescribeChunksReadError(t *testing.T) {
	errRead := errors.New("boom")
	_, err := DescribeChunks(errReader{err: errRead}, 4)
	if !errors.Is(err, errRead) {
		t.Fatalf("DescribeChunks() error = %v, want %v", err, errRead)
	}
}

func TestDescribeChunksNoProgress(t *testing.T) {
	_, err := DescribeChunks(noProgressReader{}, 4)
	if err == nil || !strings.Contains(err.Error(), "no progress") {
		t.Fatalf("DescribeChunks() error = %v, want no progress", err)
	}
}

func TestChunkLayerAnnotations(t *testing.T) {
	chunk := Chunk{
		Index:  2,
		Offset: 8,
		Size:   4,
		Digest: testDigest([]byte("test")),
	}
	got := ChunkLayerAnnotations(chunk, 5)
	want := map[string]string{
		CoveRole:                      "disk",
		CoveUncompressedSize:          "4",
		CoveUncompressedContentDigest: testDigest([]byte("test")),
		CoveChunkIndex:                "2",
		CoveChunkTotal:                "5",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ChunkLayerAnnotations() = %#v, want %#v", got, want)
	}
}

func testDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

type errReader struct {
	err error
}

func (r errReader) Read([]byte) (int, error) {
	return 0, r.err
}

type noProgressReader struct{}

func (noProgressReader) Read([]byte) (int, error) {
	return 0, nil
}

var _ io.Reader = errReader{}
var _ io.Reader = noProgressReader{}
