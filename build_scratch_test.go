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
	controlpb "github.com/tmc/vz-macos/proto/controlpb"
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

func TestBuildExecutorExecuteRunsLocalVMBuild(t *testing.T) {
	restore := stubBuildControlSender(t, func(call *int, sock string, req *controlpb.ControlRequest, timeout time.Duration, cmdType string) (*controlpb.ControlResponse, error) {
		return &controlpb.ControlResponse{Success: true}, nil
	})
	defer restore()
	root := t.TempDir()
	parentDir := filepath.Join(root, "parent")
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"disk.img":   "base image\n",
		"aux.img":    "aux",
		"hw.model":   "hw",
		"machine.id": "machine",
	} {
		if err := os.WriteFile(filepath.Join(parentDir, name), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	exec := testBuildExecutor(filepath.Join(root, "scratch"))
	exec.plan.Base = parentDir
	exec.plan.Steps = []buildPlanStep{{
		Name:                 "echo",
		Source:               "echo.vzscript",
		Data:                 []byte("echo ok\n"),
		Key:                  "sha256:" + strings.Repeat("1", 64),
		ParentDigest:         "sha256:" + strings.Repeat("2", 64),
		ScriptDigest:         "sha256:" + strings.Repeat("3", 64),
		AgentProtocolVersion: agentProtocolVersion,
		Meta:                 buildScriptMeta{Compact: "targeted"},
	}}
	exec.startGuest = func(context.Context, buildScratch) (buildGuestCleanup, error) {
		return func(context.Context) error { return nil }, nil
	}
	if err := exec.Execute(context.Background()); err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	result := exec.Result()
	if result.VMDir == "" || result.DiskPath == "" || len(result.Steps) != 1 {
		t.Fatalf("Result() = %#v, want final vm result", result)
	}
	if _, err := loadBuildCacheEntry(exec.store, exec.plan.Steps[0].Key); err != nil {
		t.Fatal(err)
	}
}

func TestBuildExecutorExecuteSecondRunUsesCache(t *testing.T) {
	restore := stubBuildControlSender(t, func(call *int, sock string, req *controlpb.ControlRequest, timeout time.Duration, cmdType string) (*controlpb.ControlResponse, error) {
		return &controlpb.ControlResponse{Success: true}, nil
	})
	defer restore()
	root := t.TempDir()
	parentDir := filepath.Join(root, "parent")
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"disk.img":   "base image\n",
		"aux.img":    "aux",
		"hw.model":   "hw",
		"machine.id": "machine",
	} {
		if err := os.WriteFile(filepath.Join(parentDir, name), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	step := buildPlanStep{
		Name:                 "echo",
		Source:               "echo.vzscript",
		Data:                 []byte("echo ok\n"),
		Key:                  "sha256:" + strings.Repeat("1", 64),
		ParentDigest:         "sha256:" + strings.Repeat("2", 64),
		ScriptDigest:         "sha256:" + strings.Repeat("3", 64),
		AgentProtocolVersion: agentProtocolVersion,
		Meta:                 buildScriptMeta{Compact: "targeted"},
	}
	storeDir := filepath.Join(root, "store")
	exec := testBuildExecutor(filepath.Join(root, "scratch"))
	exec.store = store.New(storeDir)
	exec.plan.Base = parentDir
	exec.plan.Steps = []buildPlanStep{step}
	exec.startGuest = func(context.Context, buildScratch) (buildGuestCleanup, error) {
		return func(context.Context) error { return nil }, nil
	}
	if err := exec.Execute(context.Background()); err != nil {
		t.Fatalf("first Execute(): %v", err)
	}
	entry, err := loadBuildCacheEntry(exec.store, step.Key)
	if err != nil {
		t.Fatal(err)
	}

	second := testBuildExecutor(filepath.Join(root, "scratch2"))
	second.store = store.New(storeDir)
	second.plan.Base = parentDir
	step.CacheHit = true
	step.LayerDigest = entry.LayerDigest
	second.plan.Steps = []buildPlanStep{step}
	second.startGuest = func(context.Context, buildScratch) (buildGuestCleanup, error) {
		t.Fatal("cache-hit build started guest")
		return nil, nil
	}
	if err := second.Execute(context.Background()); err != nil {
		t.Fatalf("second Execute(): %v", err)
	}
	result := second.Result()
	if result.VMDir == "" || result.DiskPath == "" || len(result.Steps) != 1 {
		t.Fatalf("Result() = %#v, want final vm result", result)
	}
}

func TestBuildExecutorExecuteCollectsStaleScratch(t *testing.T) {
	root := t.TempDir()
	parentDir := filepath.Join(root, "parent")
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parentDir, "disk.img"), []byte("base image\n"), 0644); err != nil {
		t.Fatal(err)
	}
	dead := filepath.Join(root, "scratch", "dead")
	writeScratchPID(t, dead, "999999")
	exec := testBuildExecutor(filepath.Join(root, "scratch"))
	exec.plan.Base = parentDir
	exec.plan.Steps = nil
	if err := exec.Execute(context.Background()); err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Fatalf("stale scratch still exists: %v", err)
	}
}

func TestBuildExecutorExecuteHonorsCanceledContext(t *testing.T) {
	exec := testBuildExecutor(t.TempDir())
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

func TestCreateScratchVMClonesDiskAndCopiesMetadata(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	if err := os.MkdirAll(parent, 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"disk.img":      "parent-disk",
		"aux.img":       "aux",
		"hw.model":      "hw",
		"machine.id":    "machine",
		"config.json":   "{}",
		"control.token": "token",
	} {
		if err := os.WriteFile(filepath.Join(parent, name), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	exec := testBuildExecutor(filepath.Join(root, "scratch"))
	sc, err := exec.createScratchVM(parent)
	if err != nil {
		t.Skipf("clonefile unsupported for scratch vm test: %v", err)
	}
	if filepath.Base(sc.DiskPath) != "disk.img" {
		t.Fatalf("scratch disk path = %q, want disk.img", sc.DiskPath)
	}
	for name, want := range map[string]string{
		"disk.img":      "parent-disk",
		"aux.img":       "aux",
		"hw.model":      "hw",
		"machine.id":    "machine",
		"config.json":   "{}",
		"control.token": "token",
	} {
		if got := readFile(t, filepath.Join(sc.Dir, name)); got != want {
			t.Fatalf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestCreateScratchVMRequiresParentDisk(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	if err := os.MkdirAll(parent, 0755); err != nil {
		t.Fatal(err)
	}
	exec := testBuildExecutor(filepath.Join(root, "scratch"))
	if _, err := exec.createScratchVM(parent); err == nil {
		t.Fatal("createScratchVM() error = nil, want missing disk")
	}
	assertEmptyDir(t, exec.scratchRoot)
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
