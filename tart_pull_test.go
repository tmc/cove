package main

import (
	"context"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/ociimage"
)

func TestTartPullDiskNilPlan(t *testing.T) {
	err := tartPullDisk(context.Background(), nil, pullOptions{})
	if err == nil {
		t.Fatal("tartPullDisk(nil plan) = nil, want error")
	}
	if !strings.Contains(err.Error(), "missing pull plan") {
		t.Fatalf("err = %v, want 'missing pull plan'", err)
	}
}

func TestTartPullDiskNoDiskLayers(t *testing.T) {
	plan := &pullPlan{
		Manifest: ociimage.ParsedManifest{
			Format: ociimage.FormatTart,
			Tart:   ociimage.TartManifest{},
		},
		VMDir: t.TempDir(),
	}
	err := tartPullDisk(context.Background(), plan, pullOptions{})
	if err == nil {
		t.Fatal("tartPullDisk(no layers) = nil, want error")
	}
	if !strings.Contains(err.Error(), "no disk layers") {
		t.Fatalf("err = %v, want 'no disk layers'", err)
	}
}
