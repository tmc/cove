package builddigest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBytesAndReader(t *testing.T) {
	want := "sha256:2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if got := Bytes([]byte("hello")); got != want {
		t.Fatalf("Bytes = %q, want %q", got, want)
	}
	got, err := Reader(strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("Reader: %v", err)
	}
	if got != want {
		t.Fatalf("Reader = %q, want %q", got, want)
	}
}

func TestURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello"))
	}))
	defer srv.Close()
	got, err := URL(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("URL: %v", err)
	}
	if want := Bytes([]byte("hello")); got != want {
		t.Fatalf("URL = %q, want %q", got, want)
	}
}

func TestCanonicalWriters(t *testing.T) {
	var b strings.Builder
	WriteKV(&b, "parent", "p")
	WriteMap(&b, "env", map[string]string{"B": "2", "A": "1"})
	WriteList(&b, "secret", []string{"x", "y"})
	want := "parent=p\nenv:A=1\nenv:B=2\nsecret=x\nsecret=y\n"
	if got := b.String(); got != want {
		t.Fatalf("canonical output = %q, want %q", got, want)
	}
}
