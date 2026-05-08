package metrics

import (
	"bytes"
	"strings"
	"testing"
)

// TestMaskerLargeBlob exercises Apply over a multi-megabyte stream to
// guard against accidental O(n*m) regressions in the inner loop.
func TestMaskerLargeBlob(t *testing.T) {
	m := NewMasker()
	m.AddString("needle")
	const blobSize = 4 << 20 // 4 MiB
	var b bytes.Buffer
	b.Grow(blobSize + 64)
	b.WriteString(strings.Repeat("a", blobSize/2))
	b.WriteString("needle")
	b.WriteString(strings.Repeat("b", blobSize/2))
	got := m.Apply(b.Bytes())
	if bytes.Contains(got, []byte("needle")) {
		t.Fatal("needle survived in large blob")
	}
	if !bytes.Contains(got, []byte("***")) {
		t.Fatal("mask token not found in large blob")
	}
}

// TestMaskerOverlappingSuffix verifies longest-first ordering still wins
// when two registered values share a suffix (not just a prefix).
func TestMaskerOverlappingSuffix(t *testing.T) {
	m := NewMasker()
	m.AddString("token")
	m.AddString("secrettoken")
	got := m.ApplyString("prefix secrettoken suffix token tail")
	want := "prefix *** suffix *** tail"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestMaskerNoMatchReturnsSameSlice documents the no-copy fast path:
// when no registered value occurs, Apply returns the input slice unchanged.
func TestMaskerNoMatchReturnsSameSlice(t *testing.T) {
	m := NewMasker()
	m.AddString("absent")
	in := []byte("nothing to see here")
	out := m.Apply(in)
	if &in[0] != &out[0] {
		t.Fatal("Apply allocated a copy when no match occurred")
	}
}

func BenchmarkMaskerApplyLargeBlobMiss(b *testing.B) {
	m := NewMasker()
	m.AddString("needle-not-present")
	blob := bytes.Repeat([]byte("haystack "), 1<<16)
	b.SetBytes(int64(len(blob)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.Apply(blob)
	}
}
