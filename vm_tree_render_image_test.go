package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderReachableFromImageMissingImage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ref := ImageRef{Name: "nonexistent", Tag: "v1"}
	err := renderReachableFromImage(&bytes.Buffer{}, ref, false)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want not found", err)
	}
}
