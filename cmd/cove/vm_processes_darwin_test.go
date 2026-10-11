package main

import (
	"context"
	"errors"
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

func TestFileHolderContextLateEmptyKeepsSlot(t *testing.T) {
	queries := make(chan struct{}, 1)
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	defer func() {
		close(release)
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("native worker did not finish")
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan error, 1)
	go func() {
		_, err := boundedFileHoldersContext(ctx, "disk.img", queries, func(string) ([]int, error) { close(started); <-release; defer close(done); return nil, nil })
		ready <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
	cancel()
	select {
	case err := <-ready:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("caller did not cancel")
	}
	_, err := boundedFileHoldersContext(context.Background(), "disk.img", queries, func(string) ([]int, error) { t.Error("second query started"); return nil, nil })
	if err == nil || !strings.Contains(err.Error(), "still pending") {
		t.Fatalf("err=%v", err)
	}
}

func TestFileHolderWallClockDeadline(t *testing.T) {
	queries := make(chan struct{}, 1)
	ctx := delayedHolderDeadline{context.Background(), time.Now().Add(-time.Second)}
	_, err := boundedFileHoldersContext(ctx, "disk.img", queries, func(string) ([]int, error) { t.Error("expired query admitted"); return nil, nil })
	if !errors.Is(err, context.DeadlineExceeded) || len(queries) != 0 {
		t.Fatalf("err=%v slot=%d", err, len(queries))
	}
	ctx = delayedHolderDeadline{context.Background(), time.Now().Add(30 * time.Millisecond)}
	_, err = boundedFileHoldersContext(ctx, "disk.img", queries, func(string) ([]int, error) { time.Sleep(60 * time.Millisecond); return nil, nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("late empty accepted: %v", err)
	}
}
