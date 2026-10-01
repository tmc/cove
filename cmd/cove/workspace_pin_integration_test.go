package main

import (
	"testing"
	"time"

	"github.com/tmc/cove/internal/storagepins"
	"github.com/tmc/cove/internal/vmconfig"
)

func TestWorkspaceProductionPinAdmission(t *testing.T) {
	root, guard, state, targets := workspacePinFixture(t)
	guard.Release()
	t.Setenv(vmconfig.StateDirEnv, root)
	started := processStartedAt(state.OwnerPID)
	if started.IsZero() {
		t.Fatal("current process start time unavailable")
	}
	state.OwnerStartedAt = started.Format(time.RFC3339Nano)
	runDir := targets[1].Identity.Path
	writeWorkspacePinDisposition(t, runDir, state)
	if err := defaultWorkspaceDeps().PinOwned(state, runDir); err != nil {
		t.Fatal(err)
	}
	pins, err := storagepins.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if !pins.IsPinned("vm", "workspace") || !pins.IsPinned("run", "run") || len(pins.TaskPins()) != 2 {
		t.Fatalf("owned task protection missing: %+v", pins.TaskPins())
	}
}
