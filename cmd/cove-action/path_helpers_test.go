package main

import (
	"path/filepath"
	"testing"
)

func TestDefaultLogPathFromEnviron(t *testing.T) {
	got := defaultLogPath([]string{"HOME=/tmp/x", "FOO=bar"})
	want := filepath.Join("/tmp/x", ".vz", "runs")
	if got != want {
		t.Fatalf("defaultLogPath = %q, want %q", got, want)
	}
}

func TestDefaultLogPathFallsBackToUserHomeDir(t *testing.T) {
	t.Setenv("HOME", "/tmp/fallback")
	got := defaultLogPath(nil)
	want := filepath.Join("/tmp/fallback", ".vz", "runs")
	if got != want {
		t.Fatalf("defaultLogPath = %q, want %q", got, want)
	}
}

func TestLocalImagePathBuildsNestedPath(t *testing.T) {
	cfg := config{Environ: []string{"HOME=/tmp/h"}}
	got, ok := localImagePath(cfg, "ns/sub/img:v1")
	if !ok {
		t.Fatal("expected ok=true")
	}
	want := filepath.Join("/tmp/h", ".vz", "images", "ns", "sub", "img", "v1")
	if got != want {
		t.Fatalf("localImagePath = %q, want %q", got, want)
	}
}

func TestLocalImagePathRejectsMalformedRef(t *testing.T) {
	cfg := config{Environ: []string{"HOME=/tmp/h"}}
	cases := []string{"", "noTag", ":v1", "name:"}
	for _, ref := range cases {
		if _, ok := localImagePath(cfg, ref); ok {
			t.Errorf("localImagePath(%q) ok=true, want false", ref)
		}
	}
}
