package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

func TestRenameVM_Success(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeTreeVM(t, "src", vmconfig.Config{})

	if err := RenameVM("src", "dst"); err != nil {
		t.Fatalf("RenameVM(src, dst) error = %v", err)
	}

	base := vmconfig.BaseDir()
	if _, err := os.Stat(filepath.Join(base, "src")); !os.IsNotExist(err) {
		t.Errorf("old src dir still exists after rename: err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "dst", "linux-disk.img")); err != nil {
		t.Errorf("dst missing expected payload: %v", err)
	}
}

func TestRenameVM_UpdatesActiveSymlink(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeTreeVM(t, "src", vmconfig.Config{})

	if err := vmconfig.SetActive("src"); err != nil {
		t.Fatalf("SetActive(src) = %v", err)
	}
	if got := vmconfig.ActiveName(); got != "src" {
		t.Fatalf("ActiveName() pre-rename = %q, want src", got)
	}

	if err := RenameVM("src", "dst"); err != nil {
		t.Fatalf("RenameVM(src, dst) error = %v", err)
	}

	if got := vmconfig.ActiveName(); got != "dst" {
		t.Errorf("ActiveName() post-rename = %q, want dst", got)
	}
}
