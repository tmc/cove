package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkspaceHolderBudgetRejectsLateEmpty(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"aux.img", "metadata.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	pids, err := workspaceGuestFileHoldersContext(ctx, dir, func(context.Context, string) ([]int, error) { calls++; cancel(); return nil, nil })
	if !errors.Is(err, context.Canceled) || len(pids) != 0 || calls != 1 || !strings.Contains(err.Error(), `"aux.img" (1/2) after native query`) {
		t.Fatalf("pids=%v calls=%d err=%v", pids, calls, err)
	}
}

func TestWorkspaceHolderBudgetExpiry(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"aux.img", "metadata.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	calls := 0
	_, err := workspaceGuestFileHoldersContext(ctx, dir, func(ctx context.Context, _ string) ([]int, error) { calls++; <-ctx.Done(); return nil, ctx.Err() })
	if !errors.Is(err, context.DeadlineExceeded) || calls > 1 || (calls == 1 && !strings.Contains(err.Error(), `"aux.img" (1/2) native query`)) {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestWorkspaceHolderBudgetIdentityChanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "disk.img")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	_, err := workspaceGuestFileHoldersContext(context.Background(), dir, func(context.Context, string) ([]int, error) {
		if err := os.Rename(path, path+".old"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
		return nil, nil
	})
	if err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("err=%v", err)
	}
}

func TestWorkspaceHolderInventoryValidatesBeforeQuery(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.img"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a.img", filepath.Join(dir, "z.link")); err != nil {
		t.Fatal(err)
	}
	calls := 0
	_, err := workspaceGuestFileHoldersWith(dir, func(string) ([]int, error) { calls++; return nil, nil })
	if err == nil || calls != 0 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}

func TestWorkspaceDiscardHolderTimeoutRejectsInvalid(t *testing.T) {
	for _, value := range []string{"0s", "-1s", "5m1s"} {
		err := handleWorkspaceDiscard(commandEnv{Stdout: io.Discard, Stderr: io.Discard}, []string{"-run", "example", "-y", "-holder-timeout", value})
		if err == nil || !strings.Contains(err.Error(), "holder timeout") {
			t.Fatalf("value=%s err=%v", value, err)
		}
	}
}

type delayedHolderDeadline struct {
	context.Context
	deadline time.Time
}

func (c delayedHolderDeadline) Deadline() (time.Time, bool) { return c.deadline, true }

func TestWorkspaceHolderExpiredWallClock(t *testing.T) {
	ctx := delayedHolderDeadline{context.Background(), time.Now().Add(-time.Second)}
	calls := 0
	_, err := workspaceGuestFileHoldersContext(ctx, "does-not-exist", func(context.Context, string) ([]int, error) { calls++; return nil, nil })
	if !errors.Is(err, context.DeadlineExceeded) || calls != 0 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}
