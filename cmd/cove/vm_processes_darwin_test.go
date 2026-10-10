package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBoundedFileHoldersKeepsPendingQuery(t *testing.T) {
	queries := make(chan struct{}, 1)
	release := make(chan struct{})
	defer close(release)
	pids, err := boundedFileHolders("disk.img", time.Millisecond, queries, func(string) ([]int, error) {
		<-release
		return []int{123}, nil
	})
	if len(pids) != 0 || err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("timed out query = %v, %v", pids, err)
	}
	for i := 0; i < 10; i++ {
		pids, err := boundedFileHolders("disk.img", time.Second, queries, func(string) ([]int, error) {
			t.Error("started another native query")
			return nil, nil
		})
		if len(pids) != 0 || err == nil || !strings.Contains(err.Error(), "still pending") {
			t.Fatalf("pending query = %v, %v", pids, err)
		}
	}
}

func TestBoundedFileHoldersReturnsHolders(t *testing.T) {
	queries := make(chan struct{}, 1)
	for i := 0; i < 100; i++ {
		pids, err := boundedFileHolders("disk.img", time.Second, queries, func(string) ([]int, error) {
			return []int{123}, nil
		})
		if err != nil || len(pids) != 1 || pids[0] != 123 {
			t.Fatalf("query = %v, %v", pids, err)
		}
		if len(queries) != 0 {
			t.Fatal("completed query still occupies its slot")
		}
	}
}

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
