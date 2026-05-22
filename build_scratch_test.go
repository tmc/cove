package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tmc/cove/internal/store"
	controlpb "github.com/tmc/cove/proto/controlpb"
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
	info, err := os.Stat(sc.Dir)
	if err != nil {
		t.Fatalf("stat scratch dir: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("scratch dir mode = %03o, want 700", got)
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

func TestPruneBuildScratch(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	mkdir := func(name string, age time.Duration, payload int64, pid string) string {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if pid != "" {
			if err := os.WriteFile(filepath.Join(dir, "build.pid"), []byte(pid), 0644); err != nil {
				t.Fatal(err)
			}
		}
		if payload > 0 {
			if err := os.WriteFile(filepath.Join(dir, "disk.img"), make([]byte, payload), 0644); err != nil {
				t.Fatal(err)
			}
		}
		mtime := now.Add(-age)
		if err := os.Chtimes(dir, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	young := mkdir("young", 30*time.Minute, 100, "200")     // < sanity floor: always kept
	old := mkdir("old", 10*24*time.Hour, 1000, "300")       // > older-than, dead pid: removed
	live := mkdir("live", 10*24*time.Hour, 500, "100")      // > older-than, live pid: kept
	recent := mkdir("recent", 2*time.Hour, 200, "400")      // < older-than: kept (not yet stale)
	noPID := mkdir("no-pid", 10*24*time.Hour, 250, "")      // > older-than, no pid file: removed
	veryOld := mkdir("very-old", 30*24*time.Hour, 800, "5") // > older-than, dead: removed

	isLive := func(pid int) bool { return pid == 100 }

	dryRep, err := pruneBuildScratch(root, 7*24*time.Hour, false, isLive, func() time.Time { return now })
	if err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if dryRep.Apply {
		t.Fatalf("dry-run report claims Apply=true")
	}
	for _, dir := range []string{young, old, live, recent, noPID, veryOld} {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("dry-run removed %s: %v", dir, err)
		}
	}
	// disk.img bytes + pid-file bytes per dir; old=1003 (1000+"300"), noPID=250, veryOld=801 (800+"5").
	if want := int64(1003 + 250 + 801); dryRep.BytesRemoved != want {
		t.Errorf("dry-run BytesRemoved = %d, want %d (old+noPID+veryOld)", dryRep.BytesRemoved, want)
	}

	rep, err := pruneBuildScratch(root, 7*24*time.Hour, true, isLive, func() time.Time { return now })
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	for _, dir := range []string{young, live, recent} {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("apply removed kept dir %s: %v", dir, err)
		}
	}
	for _, dir := range []string{old, noPID, veryOld} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("apply did not remove %s: %v", dir, err)
		}
	}
	if rep.BytesRemoved != int64(1003+250+801) {
		t.Errorf("apply BytesRemoved = %d, want %d", rep.BytesRemoved, 1003+250+801)
	}
	// young=103 (100+"200"), live=503 (500+"100"), recent=203 (200+"400")
	if rep.BytesKept != int64(103+503+203) {
		t.Errorf("apply BytesKept = %d, want %d", rep.BytesKept, 103+503+203)
	}
	reasons := map[string]string{}
	for _, e := range rep.Entries {
		reasons[filepath.Base(e.Dir)] = e.Reason
	}
	wantReasons := map[string]string{
		"young":    "too-young",
		"recent":   "too-young",
		"live":     "live-pid",
		"old":      "removed",
		"no-pid":   "removed",
		"very-old": "removed",
	}
	for name, want := range wantReasons {
		if got := reasons[name]; got != want {
			t.Errorf("reason for %s = %q, want %q", name, got, want)
		}
	}
}

// TestPruneBuildScratchSanityFloor confirms the 1h floor overrides
// even an absurdly small -older-than value.
func TestPruneBuildScratchSanityFloor(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	dir := filepath.Join(root, "fresh")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "build.pid"), []byte("999"), 0644); err != nil {
		t.Fatal(err)
	}
	mtime := now.Add(-30 * time.Minute) // < 1h
	if err := os.Chtimes(dir, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	rep, err := pruneBuildScratch(root, time.Second, true, func(int) bool { return false }, func() time.Time { return now })
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("dir removed despite < 1h floor: %v", err)
	}
	if rep.OlderThan < pruneBuildScratchSanityFloor {
		t.Fatalf("OlderThan = %s, want >= sanity floor %s", rep.OlderThan, pruneBuildScratchSanityFloor)
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
	if _, err := os.Stat(filepath.Join(result.VMDir, "build.pid")); !os.IsNotExist(err) {
		t.Fatalf("final build pid exists after promotion: %v", err)
	}
	if _, err := loadBuildCacheEntry(exec.store, exec.plan.Steps[0].Key); err != nil {
		t.Fatal(err)
	}
}

func TestBuildExecutorExecutePromotesFinalVM(t *testing.T) {
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
	if result.VMDir == "" {
		t.Fatalf("Result() = %#v, want final vm dir", result)
	}
	if err := gcBuildScratch(exec.scratchRoot, func(int) bool { return false }); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(result.VMDir); err != nil {
		t.Fatalf("final VM removed by scratch gc: %v", err)
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

func TestBuildExecutorExecuteMissingSecretBeforeScratch(t *testing.T) {
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
		Name:                 "secret",
		Source:               "secret.vzscript",
		Data:                 []byte("echo ok\n"),
		Key:                  "sha256:" + strings.Repeat("1", 64),
		ParentDigest:         "sha256:" + strings.Repeat("2", 64),
		ScriptDigest:         "sha256:" + strings.Repeat("3", 64),
		AgentProtocolVersion: agentProtocolVersion,
		Meta: buildScriptMeta{
			Compact: "targeted",
			Secrets: []string{"COVE_TEST_MISSING_SECRET"},
		},
	}}
	exec.startGuest = func(context.Context, buildScratch) (buildGuestCleanup, error) {
		t.Fatal("missing secret started guest")
		return nil, nil
	}
	err := exec.Execute(context.Background())
	if err == nil || !strings.Contains(err.Error(), "COVE_TEST_MISSING_SECRET") {
		t.Fatalf("Execute() = %v, want missing secret error", err)
	}
	assertEmptyDir(t, exec.scratchRoot)
	if _, err := loadBuildCacheEntry(exec.store, exec.plan.Steps[0].Key); err == nil {
		t.Fatal("cache entry written for missing secret")
	}
}

func TestBuildExecutorExecuteInvalidSecretBeforeScratch(t *testing.T) {
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
		Name:                 "secret",
		Source:               "secret.vzscript",
		Data:                 []byte("echo ok\n"),
		Key:                  "sha256:" + strings.Repeat("1", 64),
		ParentDigest:         "sha256:" + strings.Repeat("2", 64),
		ScriptDigest:         "sha256:" + strings.Repeat("3", 64),
		AgentProtocolVersion: agentProtocolVersion,
		Meta: buildScriptMeta{
			Compact: "targeted",
			Secrets: []string{"../TOKEN"},
		},
	}}
	exec.startGuest = func(context.Context, buildScratch) (buildGuestCleanup, error) {
		t.Fatal("invalid secret started guest")
		return nil, nil
	}
	err := exec.Execute(context.Background())
	if err == nil || !strings.Contains(err.Error(), "invalid secret name") {
		t.Fatalf("Execute() = %v, want invalid secret error", err)
	}
	assertEmptyDir(t, exec.scratchRoot)
}

func TestBuildExecutorSecretValueNotPersisted(t *testing.T) {
	const secretName = "COVE_TEST_TOKEN"
	const secretValue = "super-secret-build-token"
	t.Setenv(secretName, secretValue)
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
		Name:                 "secret",
		Source:               "secret.vzscript",
		Data:                 []byte("echo ok\n"),
		Key:                  "sha256:" + strings.Repeat("1", 64),
		ParentDigest:         "sha256:" + strings.Repeat("2", 64),
		ScriptDigest:         "sha256:" + strings.Repeat("3", 64),
		AgentProtocolVersion: agentProtocolVersion,
		Meta: buildScriptMeta{
			Compact: "targeted",
			Secrets: []string{secretName},
		},
	}}
	exec.startGuest = func(context.Context, buildScratch) (buildGuestCleanup, error) {
		return func(context.Context) error { return nil }, nil
	}
	if err := exec.Execute(context.Background()); err != nil {
		t.Fatalf("Execute(): %v", err)
	}
	result := exec.Result()
	if result.VMDir == "" {
		t.Fatal("Result().VMDir is empty")
	}
	for _, root := range []string{exec.store.Dir, result.VMDir} {
		if err := assertJSONFilesDoNotContain(root, secretValue); err != nil {
			t.Fatal(err)
		}
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
		compactGuest: func(context.Context, buildScratch, string) error {
			return nil
		},
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

func assertJSONFilesDoNotContain(root, value string) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), value) {
			return fmt.Errorf("%s contains secret value", path)
		}
		return nil
	})
}
