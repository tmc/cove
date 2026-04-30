package main

import (
	"context"
	"errors"
	"strings"
	"testing"
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
