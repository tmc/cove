package vmtree

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderReachableFromImageMissingImage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ref := ReachableImage{
		Ref:    "nonexistent:v1",
		Exists: func(string) bool { return false },
	}
	err := renderReachableFromImage(&bytes.Buffer{}, ref, false)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v, want not found", err)
	}
}
