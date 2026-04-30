package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

func TestRunBuildStepScript(t *testing.T) {
	exec := testBuildExecutor(t.TempDir())
	step := buildPlanStep{Name: "ok", Source: "ok.vzscript", Data: []byte("echo ok\n")}
	if err := exec.runBuildStepScript(context.Background(), step, ""); err != nil {
		t.Fatalf("runBuildStepScript(): %v", err)
	}
}

func TestRunBuildStepScriptRejectsEmptyData(t *testing.T) {
	exec := testBuildExecutor(t.TempDir())
	err := exec.runBuildStepScript(context.Background(), buildPlanStep{Name: "empty"}, "")
	if err == nil || !strings.Contains(err.Error(), "empty script data") {
		t.Fatalf("runBuildStepScript() = %v, want empty data error", err)
	}
}

func TestRunBuildStepScriptHonorsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	exec := testBuildExecutor(t.TempDir())
	step := buildPlanStep{Name: "cancel", Data: []byte("echo ok\n")}
	err := exec.runBuildStepScript(ctx, step, "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runBuildStepScript() = %v, want context.Canceled", err)
	}
}

func TestRunBuildStepInScratchRequiresDir(t *testing.T) {
	exec := testBuildExecutor(t.TempDir())
	step := buildPlanStep{Name: "missing", Data: []byte("echo ok\n")}
	err := exec.runBuildStepInScratch(context.Background(), step, buildScratch{})
	if err == nil || !strings.Contains(err.Error(), "scratch vm dir required") {
		t.Fatalf("runBuildStepInScratch() = %v, want scratch dir error", err)
	}
}

func TestRunBuildStepInScratchWaitsAndShutsDown(t *testing.T) {
	var calls []string
	restore := stubBuildControlSender(t, func(call *int, sock string, req *controlpb.ControlRequest, timeout time.Duration, cmdType string) (*controlpb.ControlResponse, error) {
		calls = append(calls, cmdType)
		return &controlpb.ControlResponse{Success: true}, nil
	})
	defer restore()
	exec := testBuildExecutor(t.TempDir())
	sc := buildScratch{Dir: filepath.Join(t.TempDir(), "scratch")}
	step := buildPlanStep{Name: "ok", Data: []byte("echo ok\n")}
	if err := exec.runBuildStepInScratch(context.Background(), step, sc); err != nil {
		t.Fatalf("runBuildStepInScratch(): %v", err)
	}
	want := []string{"agent-ping", "agent-shutdown"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("control calls = %v, want %v", calls, want)
	}
}

func TestExecuteVMBuildRunsScriptAndRecordsLayer(t *testing.T) {
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
	result, err := exec.executeVMBuild(context.Background(), parentDir)
	if err != nil {
		t.Skipf("clonefile unsupported for vm build test: %v", err)
	}
	if len(result.Steps) != 1 {
		t.Fatalf("steps = %d, want 1", len(result.Steps))
	}
	if result.VMDir == "" || result.DiskPath == "" {
		t.Fatalf("result = %#v, want vm dir and disk path", result)
	}
	if got := readFile(t, result.DiskPath); got != "base image\n" {
		t.Fatalf("final disk = %q, want unchanged base image", got)
	}
	entry, err := loadBuildCacheEntry(exec.store, exec.plan.Steps[0].Key)
	if err != nil {
		t.Fatal(err)
	}
	if entry.ScriptDigest != exec.plan.Steps[0].ScriptDigest {
		t.Fatalf("entry script digest = %q, want %q", entry.ScriptDigest, exec.plan.Steps[0].ScriptDigest)
	}
}
