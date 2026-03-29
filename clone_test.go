package main

import (
	"os"
	"testing"
)

func TestSupportsClonefile(t *testing.T) {
	dir := t.TempDir()

	// TempDir on macOS is typically APFS, so clonefile should work.
	got := SupportsClonefile(dir)
	if !got {
		t.Logf("clonefile not supported in %s (may be non-APFS)", dir)
	}
}

func TestSupportsClonefileBadDir(t *testing.T) {
	if SupportsClonefile("/nonexistent/dir") {
		t.Error("expected false for nonexistent directory")
	}
}

func TestCloneFileCreatesIdenticalContent(t *testing.T) {
	dir := t.TempDir()

	src := dir + "/src.txt"
	dst := dir + "/dst.txt"
	content := []byte("hello clonefile")
	if err := os.WriteFile(src, content, 0644); err != nil {
		t.Fatal(err)
	}

	if err := cloneFile(src, dst); err != nil {
		t.Skipf("clonefile not available: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Errorf("clone content = %q, want %q", got, content)
	}
}
