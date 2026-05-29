package main

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestTeardownGuestProxyOnRuntimeNoStateFile(t *testing.T) {
	rt := newFakeProxyRuntime(t.TempDir(), false)
	if err := teardownGuestProxyOnRuntime(context.Background(), rt); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}

func TestTeardownGuestProxyOnRuntimeCapturedStageRemovesFile(t *testing.T) {
	rt := newFakeProxyRuntime(t.TempDir(), false)
	state := &proxyState{Stage: proxyStateCaptured, Platform: proxyPlatformMacOS}
	if err := saveProxyState(rt.VMDir(), state); err != nil {
		t.Fatalf("saveProxyState: %v", err)
	}
	if err := teardownGuestProxyOnRuntime(context.Background(), rt); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if _, err := os.Stat(proxyStatePath(rt.VMDir())); !os.IsNotExist(err) {
		t.Fatalf("state file still exists: err = %v", err)
	}
}

func TestTeardownGuestProxyOnRuntimeUnknownStage(t *testing.T) {
	rt := newFakeProxyRuntime(t.TempDir(), false)
	state := &proxyState{Stage: "weird-stage", Platform: proxyPlatformMacOS}
	if err := saveProxyState(rt.VMDir(), state); err != nil {
		t.Fatalf("saveProxyState: %v", err)
	}
	err := teardownGuestProxyOnRuntime(context.Background(), rt)
	if err == nil || !strings.Contains(err.Error(), "unknown proxy state stage") {
		t.Fatalf("err = %v, want unknown stage", err)
	}
}
