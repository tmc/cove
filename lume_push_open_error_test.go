package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteTarGzipStreamMissingDisk(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.img")
	err := writeTarGzipStream(&bytes.Buffer{}, missing)
	if err == nil || !strings.Contains(err.Error(), "open disk") {
		t.Fatalf("err = %v, want open disk", err)
	}
}
