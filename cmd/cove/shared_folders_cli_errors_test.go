package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAddSharedFolderTagExistsSentinel(t *testing.T) {
	dir := t.TempDir()
	hostA := filepath.Join(dir, "a")
	hostB := filepath.Join(dir, "b")
	for _, p := range []string{hostA, hostB} {
		if err := os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := addSharedFolderEntry(dir, hostA, "shared", false); err != nil {
		t.Fatalf("first addSharedFolderEntry: %v", err)
	}
	_, _, err := addSharedFolderEntry(dir, hostB, "shared", false)
	if !errors.Is(err, ErrSharedFolderTagExists) {
		t.Fatalf("err = %v, want errors.Is(err, ErrSharedFolderTagExists)", err)
	}
}

func TestRemoveSharedFolderNotFoundSentinel(t *testing.T) {
	dir := t.TempDir()
	err := handleVMSharedFolderRemove(dir, "ghost-tag")
	if !errors.Is(err, ErrSharedFolderNotFound) {
		t.Fatalf("err = %v, want errors.Is(err, ErrSharedFolderNotFound)", err)
	}
}
