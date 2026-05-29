package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/buildscratch"
)

func TestCleanupScratchEmptyDirNoOp(t *testing.T) {
	exec := testBuildExecutor(t.TempDir())
	if err := exec.cleanupScratch(buildscratch.Scratch{}); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}

func TestCleanupScratchRemoveError(t *testing.T) {
	exec := testBuildExecutor(t.TempDir())
	root := t.TempDir()
	target := filepath.Join(root, "scratch")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, "f"), []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := os.Chmod(root, 0500); err != nil {
		t.Fatalf("Chmod root: %v", err)
	}
	t.Cleanup(func() { os.Chmod(root, 0755) })

	err := exec.cleanupScratch(buildscratch.Scratch{Dir: target})
	if err == nil || !strings.Contains(err.Error(), "remove build scratch") {
		t.Fatalf("err = %v, want remove build scratch", err)
	}
}
