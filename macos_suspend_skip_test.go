package main

import (
	"os"
	"testing"
)

func TestCheckSuspendConfigMatchMissingFileReturnsNil(t *testing.T) {
	old := vmDir
	t.Cleanup(func() { vmDir = old })
	vmDir = t.TempDir()
	if err := checkSuspendConfigMatch(); err != nil {
		t.Fatalf("err = %v, want nil for missing config", err)
	}
}

func TestCheckSuspendConfigMatchCorruptFileReturnsNil(t *testing.T) {
	old := vmDir
	t.Cleanup(func() { vmDir = old })
	vmDir = t.TempDir()
	if err := os.WriteFile(suspendConfigPath(), []byte("not-json"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := checkSuspendConfigMatch(); err != nil {
		t.Fatalf("err = %v, want nil for corrupt config", err)
	}
}
