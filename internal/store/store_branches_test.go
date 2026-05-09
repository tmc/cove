package store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
)

func digestOf(b []byte) string {
	h := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(h[:])
}

func TestPutRejectsInvalidDigests(t *testing.T) {
	tests := []struct {
		name   string
		digest string
		want   string
	}{
		{name: "no colon", digest: "deadbeef", want: "invalid digest"},
		{name: "empty algo", digest: ":abc", want: "invalid digest"},
		{name: "empty hex", digest: "sha256:", want: "invalid digest"},
		{name: "unsupported algo", digest: "sha512:" + strings.Repeat("0", 64), want: "unsupported digest"},
		{name: "wrong hex length", digest: "sha256:abcd", want: "invalid digest"},
		{name: "non-hex chars", digest: "sha256:" + strings.Repeat("z", 64), want: "invalid digest"},
	}
	s := Store{Dir: t.TempDir()}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := s.Put(tt.digest, 0, bytes.NewReader(nil))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Put(%q) err=%v, want substring %q", tt.digest, err, tt.want)
			}
		})
	}
}

func TestPutRejectsNegativeSize(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	err := s.Put(digestOf([]byte("hi")), -1, bytes.NewReader([]byte("hi")))
	if err == nil || !strings.Contains(err.Error(), "negative size") {
		t.Errorf("Put(neg size) err=%v, want negative-size error", err)
	}
}

func TestPutRejectsSizeMismatch(t *testing.T) {
	body := []byte("hello")
	s := Store{Dir: t.TempDir()}
	err := s.Put(digestOf(body), int64(len(body)+5), bytes.NewReader(body))
	if err == nil || !strings.Contains(err.Error(), "size") {
		t.Errorf("Put(size mismatch) err=%v, want size-mismatch error", err)
	}
}

func TestPutRejectsDigestMismatch(t *testing.T) {
	body := []byte("payload")
	wrong := digestOf([]byte("not-the-payload"))
	s := Store{Dir: t.TempDir()}
	err := s.Put(wrong, int64(len(body)), bytes.NewReader(body))
	if err == nil || !strings.Contains(err.Error(), "digest") {
		t.Errorf("Put(digest mismatch) err=%v, want digest-mismatch error", err)
	}
}

func TestEnsureRejectsNilFetch(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	err := s.Ensure(context.Background(), digestOf([]byte("x")), 1, nil)
	if err == nil || !strings.Contains(err.Error(), "nil fetch") {
		t.Errorf("Ensure(nil fetch) err=%v, want nil-fetch error", err)
	}
}

func TestEnsurePropagatesFetchError(t *testing.T) {
	sentinel := errors.New("fetch boom")
	s := Store{Dir: t.TempDir()}
	err := s.Ensure(context.Background(), digestOf([]byte("x")), 1, func(context.Context) (io.ReadCloser, error) {
		return nil, sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("Ensure err=%v, want sentinel %v", err, sentinel)
	}
}
