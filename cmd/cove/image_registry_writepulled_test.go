package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestWritePulledLayerOpenError(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "missing-parent", "out.bin")
	err := writePulledLayer(bad, "manifest.json", bytes.NewReader([]byte("x")))
	if err == nil || !strings.Contains(err.Error(), "open manifest.json") {
		t.Fatalf("err = %v, want open manifest.json", err)
	}
}

func TestWritePulledLayerHappyPath(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "out.bin")
	want := []byte("hello-layer")
	if err := writePulledLayer(dst, "config.json", bytes.NewReader(want)); err != nil {
		t.Fatalf("writePulledLayer: %v", err)
	}
}
