package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDarwinOpenVnodePathsIncludesOpenFile(t *testing.T) {
	if err := ensureLibproc(); err != nil {
		t.Fatalf("ensureLibproc: %v", err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "disk.img")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	paths, err := darwinOpenVnodePaths(int32(os.Getpid()), 64)
	if err != nil {
		t.Fatalf("darwinOpenVnodePaths: %v", err)
	}
	want := vmProcessRealPath(path)
	for _, got := range paths {
		if vmProcessRealPath(got) == want {
			return
		}
	}
	t.Fatalf("open paths did not include %s:\n%v", path, paths)
}

func TestDarwinFileHoldersIncludesOpenFile(t *testing.T) {
	if err := ensureLibproc(); err != nil {
		t.Fatalf("ensureLibproc: %v", err)
	}
	path := filepath.Join(t.TempDir(), "disk.img")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	holders, err := darwinFileHolders(path)
	if err != nil {
		t.Fatalf("darwinFileHolders: %v", err)
	}
	pid := os.Getpid()
	for _, holder := range holders {
		if holder == pid {
			return
		}
	}
	t.Fatalf("file holders for %s = %v, want current pid %d", path, holders, pid)
}
