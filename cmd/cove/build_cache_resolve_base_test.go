package main

import (
	"context"
	"strings"
	"testing"
)

func TestResolveBuildBaseDigestParseError(t *testing.T) {
	_, _, err := resolveBuildBaseDigest(context.Background(), "::not-a-ref::")
	if err == nil {
		t.Fatal("err = nil, want parse error")
	}
}

func TestResolveBuildBaseDigestRefWithDigestShortCircuits(t *testing.T) {
	want := "sha256:" + strings.Repeat("a", 64)
	ref, digest, err := resolveBuildBaseDigest(context.Background(), "ghcr.io/acme/base@"+want)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if digest != want {
		t.Fatalf("digest = %q, want %q", digest, want)
	}
	if ref.Digest != want {
		t.Fatalf("ref.Digest = %q, want %q", ref.Digest, want)
	}
}
