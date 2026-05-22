package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/cove/internal/vmconfig"
)

func writeTreeVM(t *testing.T, name string, cfg vmconfig.Config) {
	t.Helper()

	dir := filepath.Join(vmconfig.BaseDir(), name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "linux-disk.img"), []byte("disk"), 0644); err != nil {
		t.Fatalf("WriteFile(%s/linux-disk.img) error = %v", name, err)
	}
	if err := vmconfig.Save(dir, &cfg); err != nil {
		t.Fatalf("vmconfig.Save(%s) error = %v", name, err)
	}
}
