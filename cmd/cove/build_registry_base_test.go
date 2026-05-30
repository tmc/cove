package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMaterializeBuildRegistryBasePullsCoveManifest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	diskData := []byte("registry base disk")
	manifest, blobs := pullCompressedTestManifest(t, diskData)
	srv := pullTestRegistry(t, manifest, blobs)
	defer srv.Close()

	dir, cleanup, err := materializeBuildRegistryBase(context.Background(), "ghcr.io/me/dev-vm:v1", buildOptions{RegistryBaseURL: srv.URL})
	if err != nil {
		t.Fatalf("materializeBuildRegistryBase(): %v", err)
	}
	if cleanup == nil {
		t.Fatal("cleanup = nil")
	}
	got, err := os.ReadFile(filepath.Join(dir, "disk.img"))
	if err != nil {
		t.Fatalf("ReadFile(disk.img): %v", err)
	}
	if !bytes.Equal(got, diskData) {
		t.Fatalf("disk = %q, want %q", got, diskData)
	}
	for _, name := range []string{"aux.img", "hw.model", "machine.id", "disk.provenance"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
	}
	if err := cleanup(); err != nil {
		t.Fatalf("cleanup(): %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("materialized dir stat = %v, want not exist", err)
	}
}
