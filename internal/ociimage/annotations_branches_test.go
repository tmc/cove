package ociimage

import (
	"strings"
	"testing"
)

func TestNormalizeLayerAnnotationsMissingFields(t *testing.T) {
	complete := map[string]string{
		CoveUncompressedSize:          "1024",
		CoveUncompressedContentDigest: "sha256:cafebabe",
		CoveChunkIndex:                "0",
		CoveChunkTotal:                "1",
	}
	tests := []struct {
		name    string
		drop    string
		wantSub string
	}{
		{name: "missing uncompressed size", drop: CoveUncompressedSize, wantSub: "uncompressed-size"},
		{name: "missing content digest", drop: CoveUncompressedContentDigest, wantSub: "uncompressed-content-digest"},
		{name: "missing chunk index", drop: CoveChunkIndex, wantSub: "chunk-index"},
		{name: "missing chunk total", drop: CoveChunkTotal, wantSub: "chunk-total"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := make(map[string]string, len(complete))
			for k, v := range complete {
				if k == tt.drop {
					continue
				}
				in[k] = v
			}
			_, err := NormalizeLayerAnnotations(in)
			if err == nil {
				t.Fatalf("NormalizeLayerAnnotations missing %q error = nil, want non-nil", tt.drop)
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("NormalizeLayerAnnotations error = %v, want substring %q", err, tt.wantSub)
			}
		})
	}
}

func TestNormalizeLayerAnnotationsParseErrors(t *testing.T) {
	base := map[string]string{
		CoveUncompressedSize:          "1024",
		CoveUncompressedContentDigest: "sha256:cafebabe",
		CoveChunkIndex:                "0",
		CoveChunkTotal:                "1",
	}
	tests := []struct {
		name string
		key  string
		bad  string
	}{
		{name: "bad size", key: CoveUncompressedSize, bad: "abc"},
		{name: "bad index", key: CoveChunkIndex, bad: "x"},
		{name: "bad total", key: CoveChunkTotal, bad: "y"},
		{name: "bad zero chunk bool", key: CoveZeroChunk, bad: "notabool"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := make(map[string]string, len(base)+1)
			for k, v := range base {
				in[k] = v
			}
			in[tt.key] = tt.bad
			if _, err := NormalizeLayerAnnotations(in); err == nil {
				t.Fatalf("NormalizeLayerAnnotations bad %s = nil, want error", tt.key)
			}
		})
	}
}

func TestNormalizeLayerAnnotationsZeroChunkParsed(t *testing.T) {
	in := map[string]string{
		CoveUncompressedSize:          "1024",
		CoveUncompressedContentDigest: "sha256:cafebabe",
		CoveChunkIndex:                "0",
		CoveChunkTotal:                "1",
		CoveZeroChunk:                 "true",
	}
	got, err := NormalizeLayerAnnotations(in)
	if err != nil {
		t.Fatalf("NormalizeLayerAnnotations: %v", err)
	}
	if !got.ZeroChunk {
		t.Fatal("ZeroChunk = false, want true")
	}
}

func TestMissingAnnotationErrorWithoutLumeAlias(t *testing.T) {
	// A key not in coveToLume must produce a single-key message.
	err := missingAnnotationError("org.tmc.cove.no-such-thing")
	if err == nil {
		t.Fatal("missingAnnotationError = nil, want error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "no-such-thing") {
		t.Fatalf("missingAnnotationError = %q, want substring no-such-thing", msg)
	}
	if strings.Contains(msg, " or ") {
		t.Fatalf("missingAnnotationError = %q, want no Lume alias clause", msg)
	}
}
