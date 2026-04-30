package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/vz-macos/internal/store"
)

func TestBuildScratchCreateWritesMetadata(t *testing.T) {
	root := t.TempDir()
	exec := testBuildExecutor(root)
	sc, err := exec.createScratch("")
	if err != nil {
		t.Fatalf("createScratch(): %v", err)
	}
	if sc.Dir == "" || !strings.HasPrefix(sc.Dir, root) {
		t.Fatalf("scratch dir = %q, want under %q", sc.Dir, root)
	}
	if got := strings.TrimSpace(readFile(t, sc.PIDPath)); got != "1234" {
		t.Fatalf("build.pid = %q, want 1234", got)
	}
	var meta buildScratchMeta
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(sc.Dir, "build.json"))), &meta); err != nil {
		t.Fatalf("unmarshal build.json: %v", err)
	}
	if meta.ID != sc.ID {
		t.Fatalf("metadata ID = %q, want %q", meta.ID, sc.ID)
	}
	if meta.PID != 1234 {
		t.Fatalf("metadata PID = %d, want 1234", meta.PID)
	}
	if meta.PlanDigest == "" {
		t.Fatal("metadata PlanDigest is empty")
	}
	if !meta.CreatedAt.Equal(exec.now().UTC()) {
		t.Fatalf("metadata CreatedAt = %s, want %s", meta.CreatedAt, exec.now().UTC())
	}
}

func TestBuildScratchCleanupRemovesDir(t *testing.T) {
	exec := testBuildExecutor(t.TempDir())
	sc, err := exec.createScratch("")
	if err != nil {
		t.Fatalf("createScratch(): %v", err)
	}
	if err := exec.cleanupScratch(sc); err != nil {
		t.Fatalf("cleanupScratch(): %v", err)
	}
	if _, err := os.Stat(sc.Dir); !os.IsNotExist(err) {
		t.Fatalf("scratch dir exists after cleanup: %v", err)
	}
	if err := exec.cleanupScratch(sc); err != nil {
		t.Fatalf("cleanupScratch(already removed): %v", err)
	}
}

func TestGCBuildScratch(t *testing.T) {
	root := t.TempDir()
	writeScratchPID(t, filepath.Join(root, "live"), "100\n")
	writeScratchPID(t, filepath.Join(root, "dead"), "200\n")
	writeScratchPID(t, filepath.Join(root, "bad"), "not-a-pid\n")
	if err := gcBuildScratch(root, func(pid int) bool { return pid == 100 }); err != nil {
		t.Fatalf("gcBuildScratch(): %v", err)
	}
	for _, name := range []string{"live", "bad"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Fatalf("%s removed unexpectedly: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "dead")); !os.IsNotExist(err) {
		t.Fatalf("dead scratch still exists: %v", err)
	}
}

func TestBuildExecutorExecuteStillNotImplemented(t *testing.T) {
	exec := testBuildExecutor(t.TempDir())
	err := exec.Execute(context.Background())
	if !errors.Is(err, errBuildExecutionNotImplemented) {
		t.Fatalf("Execute() = %v, want %v", err, errBuildExecutionNotImplemented)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := exec.Execute(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute(canceled) = %v, want context.Canceled", err)
	}
}

func TestHandleBuildDryRunHasNoScratchSideEffects(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	script := filepath.Join(home, "hello.vzscript")
	if err := os.WriteFile(script, []byte("exec echo hello\n"), 0644); err != nil {
		t.Fatal(err)
	}
	err := handleBuild([]string{
		"test-image",
		"--base", "ghcr.io/acme/base@sha256:" + strings.Repeat("a", 64),
		"--script", script,
		"--dry-run",
	})
	if err != nil {
		t.Fatalf("handleBuild(): %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".vz", "build-scratch")); !os.IsNotExist(err) {
		t.Fatalf("dry-run created build-scratch: %v", err)
	}
}

func TestCreateScratchClonesParentDisk(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent.img")
	if err := os.WriteFile(parent, []byte("parent-disk"), 0644); err != nil {
		t.Fatal(err)
	}
	exec := testBuildExecutor(filepath.Join(root, "scratch"))
	sc, err := exec.createScratch(parent)
	if err != nil {
		t.Skipf("clonefile unsupported for scratch test: %v", err)
	}
	if got := readFile(t, sc.DiskPath); got != "parent-disk" {
		t.Fatalf("scratch disk = %q, want parent-disk", got)
	}
}

func testBuildExecutor(root string) *buildExecutor {
	return &buildExecutor{
		plan: buildPlan{
			Name:         "test",
			Base:         "ghcr.io/acme/base@sha256:" + strings.Repeat("a", 64),
			ParentDigest: "sha256:" + strings.Repeat("a", 64),
			Steps:        []buildPlanStep{{Name: "one", Key: "sha256:" + strings.Repeat("b", 64)}},
		},
		opts:        buildOptions{KeepIntermediate: true},
		store:       store.New(filepath.Join(root, "store")),
		scratchRoot: root,
		now: func() time.Time {
			return time.Date(2026, 4, 30, 3, 30, 0, 0, time.UTC)
		},
		pid: 1234,
	}
}

func writeScratchPID(t *testing.T, dir, pid string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "build.pid"), []byte(pid), 0644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
