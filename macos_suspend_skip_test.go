package main

import (
	"os"
	"testing"

	"github.com/tmc/cove/internal/vmrun"
)

func TestCheckSuspendConfigMatchMissingFileReturnsNil(t *testing.T) {
	hc := vmrun.HostConfig{VMDir: t.TempDir()}
	if err := checkSuspendConfigMatchForRun(vmrun.RunConfig{}, hc); err != nil {
		t.Fatalf("err = %v, want nil for missing config", err)
	}
}

func TestCheckSuspendConfigMatchCorruptFileReturnsNil(t *testing.T) {
	hc := vmrun.HostConfig{VMDir: t.TempDir()}
	if err := os.WriteFile(suspendConfigPathForVM(hc.VMDir), []byte("not-json"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := checkSuspendConfigMatchForRun(vmrun.RunConfig{}, hc); err != nil {
		t.Fatalf("err = %v, want nil for corrupt config", err)
	}
}
