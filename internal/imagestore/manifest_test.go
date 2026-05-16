package imagestore

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManifestRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	ref := Ref{Name: "base/linux", Tag: "v1"}
	if err := os.MkdirAll(ref.Path(), 0o700); err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	want := &Manifest{
		SchemaVersion: 1,
		Name:          ref.Name,
		Tag:           ref.Tag,
		OSType:        "Linux",
		DiskSHA256:    "abc",
		DiskSize:      123,
		CreatedAt:     created,
	}
	if err := WriteManifest(ref.Path(), want); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ref.Path(), "manifest.json.tmp")); !os.IsNotExist(err) {
		t.Fatalf("temp manifest exists or stat failed: %v", err)
	}
	got, err := LoadManifest(ref)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if got.Name != want.Name || got.Tag != want.Tag || got.OSType != want.OSType || got.DiskSHA256 != want.DiskSHA256 || got.DiskSize != want.DiskSize || !got.CreatedAt.Equal(created) {
		t.Fatalf("LoadManifest = %#v, want %#v", got, want)
	}
}
