package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestImageSearchLabelsMergesFileAndManifest(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ref, err := ParseImageRef("labels-test:v1")
	if err != nil {
		t.Fatalf("ParseImageRef: %v", err)
	}
	if err := os.MkdirAll(ref.Path(), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	// LABELS file: one tag per line, blank lines and dupes ignored.
	if err := os.WriteFile(filepath.Join(ref.Path(), "LABELS"), []byte("alpha\n\nbeta\nalpha\n"), 0o644); err != nil {
		t.Fatalf("write LABELS: %v", err)
	}
	// Manifest with labels as map (alphabetised key=value entries).
	manifest := []byte(`{"labels":{"role":"web","env":"prod"}}`)
	if err := os.WriteFile(filepath.Join(ref.Path(), "manifest.json"), manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	got := imageSearchLabels(ref)
	want := []string{"alpha", "beta", "env=prod", "role=web"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("imageSearchLabels = %#v, want %#v", got, want)
	}
}

func TestImageSearchLabelsManifestArrayForm(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ref, _ := ParseImageRef("array-form:v2")
	if err := os.MkdirAll(ref.Path(), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	manifest := []byte(`{"labels":["one","two","one"]}`)
	if err := os.WriteFile(filepath.Join(ref.Path(), "manifest.json"), manifest, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	got := imageSearchLabels(ref)
	want := []string{"one", "two"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("imageSearchLabels = %#v, want %#v", got, want)
	}
}
