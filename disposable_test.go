package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

func TestDisposableCloneNameRoundTrip(t *testing.T) {
	now := time.Date(2026, 3, 29, 12, 34, 56, 0, time.Local)
	got := disposableCloneName("/tmp/research-base", now)
	want := "research-base-d-20260329-123456"
	if got != want {
		t.Fatalf("disposableCloneName() = %q, want %q", got, want)
	}

	base, ts, ok := parseDisposableCloneName(got)
	if !ok {
		t.Fatalf("parseDisposableCloneName(%q) = ok=false", got)
	}
	if base != "research-base" {
		t.Fatalf("parseDisposableCloneName(%q) base = %q, want %q", got, base, "research-base")
	}
	if !ts.Equal(now) {
		t.Fatalf("parseDisposableCloneName(%q) time = %v, want %v", got, ts, now)
	}
}

func TestSetupDisposableCloneUsesInjectedClone(t *testing.T) {
	// HOME must be set BEFORE any vmconfig.BaseDir() call. An earlier
	// version of this test called os.RemoveAll(vmconfig.BaseDir()) here,
	// which resolved against the real HOME and wiped the developer's VM
	// tree mid-install when run in parallel with `cove up`.
	home := t.TempDir()
	t.Setenv("HOME", home)

	fixedNow := time.Date(2026, 3, 29, 12, 34, 56, 0, time.Local)
	var gotOpts CloneOptions
	clone := func(opts CloneOptions) error {
		gotOpts = opts
		target := vmconfig.Path(opts.Target)
		if err := os.MkdirAll(target, 0755); err != nil {
			return err
		}
		return os.WriteFile(proxyStatePath(target), []byte("{}\n"), 0644)
	}

	got, err := SetupDisposableClone(DisposableSetupOptions{
		Source:        "research-base",
		Linked:        true,
		CopyMachineID: false,
		Now: func() time.Time {
			return fixedNow
		},
		Clone: clone,
	})
	if err != nil {
		t.Fatalf("SetupDisposableClone() error = %v", err)
	}

	wantName := "research-base-d-20260329-123456"
	if got.Name != wantName {
		t.Fatalf("SetupDisposableClone() name = %q, want %q", got.Name, wantName)
	}
	if got.Path != vmconfig.Path(wantName) {
		t.Fatalf("SetupDisposableClone() path = %q, want %q", got.Path, vmconfig.Path(wantName))
	}
	if got.Source != "research-base" {
		t.Fatalf("SetupDisposableClone() source = %q, want %q", got.Source, "research-base")
	}
	if !got.CreatedAt.Equal(fixedNow) {
		t.Fatalf("SetupDisposableClone() createdAt = %v, want %v", got.CreatedAt, fixedNow)
	}
	if gotOpts.Source != "research-base" || gotOpts.Target != wantName || !gotOpts.Linked || gotOpts.CopyMachineID {
		t.Fatalf("SetupDisposableClone() clone opts = %#v", gotOpts)
	}
	if _, err := os.Stat(proxyStatePath(got.Path)); !os.IsNotExist(err) {
		t.Fatalf("SetupDisposableClone() left proxy state behind: %v", err)
	}
}

func TestCleanupDisposableClone(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "clone")
	if err := os.MkdirAll(target, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "disk.img"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := CleanupDisposableClone(target); err != nil {
		t.Fatalf("CleanupDisposableClone() error = %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("CleanupDisposableClone() left directory behind: %v", err)
	}
}

func TestGCDisposableClones(t *testing.T) {
	// HOME must be set BEFORE any vmconfig.BaseDir() call — see
	// TestSetupDisposableCloneUsesInjectedClone above for the original
	// regression context.
	home := t.TempDir()
	t.Setenv("HOME", home)

	baseDir := vmconfig.BaseDir()
	now := time.Date(2026, 3, 29, 12, 0, 0, 0, time.Local)
	oldName := disposableCloneName("research-base", now.Add(-48*time.Hour))
	newName := disposableCloneName("research-base", now.Add(-2*time.Hour))
	activeName := disposableCloneName("research-base", now.Add(-72*time.Hour))
	oldPath := filepath.Join(baseDir, oldName)
	newPath := filepath.Join(baseDir, newName)
	activePath := filepath.Join(baseDir, activeName)
	for _, path := range []string{oldPath, newPath, activePath} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}

	got, err := GCDisposableClones(DisposableGCOptions{
		BaseDir:   baseDir,
		OlderThan: 24 * time.Hour,
		Now: func() time.Time {
			return now
		},
		IsActive: func(path string) bool {
			return path == activePath
		},
		RemoveAll: func(path string) error {
			return os.RemoveAll(path)
		},
	})
	if err != nil {
		t.Fatalf("GCDisposableClones() error = %v", err)
	}
	if got.Scanned != 3 {
		t.Fatalf("GCDisposableClones() scanned = %d, want 3", got.Scanned)
	}
	if got.SkippedAlive != 1 {
		t.Fatalf("GCDisposableClones() skippedAlive = %d, want 1", got.SkippedAlive)
	}
	if got.Candidates != 1 {
		t.Fatalf("GCDisposableClones() candidates = %d, want 1", got.Candidates)
	}
	if got.Removed != 1 {
		t.Fatalf("GCDisposableClones() removed = %d, want 1", got.Removed)
	}
	if len(got.Paths) != 1 || got.Paths[0] != oldPath {
		t.Fatalf("GCDisposableClones() paths = %#v, want [%q]", got.Paths, oldPath)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old disposable clone still exists: %v", err)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("young disposable clone missing: %v", err)
	}
	if _, err := os.Stat(activePath); err != nil {
		t.Fatalf("active disposable clone missing: %v", err)
	}
}

func TestHandleGCCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	baseDir := vmconfig.BaseDir()
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		t.Fatalf("mkdir vm base dir: %v", err)
	}

	oldName := disposableCloneName("research-base", time.Now().Add(-48*time.Hour))
	oldPath := filepath.Join(baseDir, oldName)
	if err := os.MkdirAll(oldPath, 0755); err != nil {
		t.Fatalf("mkdir old disposable clone: %v", err)
	}

	out, err := captureStdoutResult(t, func() error {
		return handleGCCommand([]string{"-dry-run", "-older-than", "24h"})
	})
	if err != nil {
		t.Fatalf("handleGCCommand(dry-run) error = %v", err)
	}
	if !strings.Contains(out, "would remove "+oldPath) {
		t.Fatalf("dry-run output = %q, want removal line for %q", out, oldPath)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("dry-run removed clone unexpectedly: %v", err)
	}

	out, err = captureStdoutResult(t, func() error {
		return handleGCCommand([]string{"-older-than", "24h"})
	})
	if err != nil {
		t.Fatalf("handleGCCommand(remove) error = %v", err)
	}
	if !strings.Contains(out, "removed "+oldPath) {
		t.Fatalf("remove output = %q, want removal line for %q", out, oldPath)
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old disposable clone still exists: %v", err)
	}
}
