package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tmc/cove/internal/mutationguard"
	"github.com/tmc/cove/internal/storagepins"
)

func workspacePinFixture(t *testing.T) (string, *mutationguard.Guard, taskDisposition, []workspaceTaskPinTarget) {
	t.Helper()
	root := resolvePath(t.TempDir())
	runPath := filepath.Join(root, "runs", "run")
	guestPath := filepath.Join(root, "vms", "workspace.covevm")
	for _, path := range []string{runPath, guestPath} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	source, err := identifyTaskGuest(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	guest, err := identifyTaskGuest(guestPath)
	if err != nil {
		t.Fatal(err)
	}
	run, err := identifyTaskGuest(runPath)
	if err != nil {
		t.Fatal(err)
	}
	state := taskDisposition{Version: 1, RunID: "run", AttemptID: "attempt", Generation: "0123456789abcdef0123456789abcdef", OwnerPID: os.Getpid(), OwnerStartedAt: "2026-10-01T12:00:00Z", Source: source.Path, SourceGuest: &source, Policy: "discard-success", State: "preparing", Guest: &guest, Owned: true, Sequence: 1, UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	writeWorkspacePinDisposition(t, runPath, state)
	guard, err := mutationguard.Acquire(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { guard.Release() })
	return root, guard, state, []workspaceTaskPinTarget{{Category: "vm", ID: "workspace", Identity: guest}, {Category: "run", ID: "run", Identity: run}}
}
func writeWorkspacePinDisposition(t *testing.T, dir string, state taskDisposition) {
	t.Helper()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "task-disposition.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}
func workspacePinTestOwner(taskDisposition, string) error { return nil }

func TestWorkspaceTaskPinsBindDurableGuestAndRun(t *testing.T) {
	tests := []struct {
		name   string
		change func(*taskDisposition, []workspaceTaskPinTarget)
	}{
		{"guest-id", func(_ *taskDisposition, p []workspaceTaskPinTarget) { p[0].ID = "other" }},
		{"guest-inode", func(_ *taskDisposition, p []workspaceTaskPinTarget) { p[0].Identity.Inode++ }},
		{"run-id", func(_ *taskDisposition, p []workspaceTaskPinTarget) { p[1].ID = "other" }},
		{"run-path", func(_ *taskDisposition, p []workspaceTaskPinTarget) { p[1].Identity.Path = "/other" }},
		{"run-inode", func(_ *taskDisposition, p []workspaceTaskPinTarget) { p[1].Identity.Inode++ }},
		{"durable-owner", func(s *taskDisposition, _ []workspaceTaskPinTarget) { s.OwnerPID++ }},
		{"durable-generation", func(s *taskDisposition, _ []workspaceTaskPinTarget) {
			s.Generation = "1123456789abcdef0123456789abcdef"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, guard, state, targets := workspacePinFixture(t)
			tt.change(&state, targets)
			if err := updateWorkspaceTaskPins(root, guard, state, state.Generation, targets, false, workspacePinTestOwner, nil); err == nil {
				t.Fatal("accepted unbound pin authority")
			}
			pins, err := storagepins.Load(root)
			if err != nil || len(pins.TaskPins()) != 0 {
				t.Fatalf("pins %v, error %v", pins, err)
			}
		})
	}
}

func TestWorkspaceTaskPinsRetainUntilConfirmedDiscard(t *testing.T) {
	root, guard, state, targets := workspacePinFixture(t)
	if err := updateWorkspaceTaskPins(root, guard, state, state.Generation, targets, false, workspacePinTestOwner, nil); err != nil {
		t.Fatal(err)
	}
	if err := updateWorkspaceTaskPins(root, guard, state, state.Generation, targets, true, workspacePinTestOwner, nil); err == nil {
		t.Fatal("released preparing guest pins")
	}
	state.State = "discarded"
	state.TaskSucceeded = true
	if err := updateWorkspaceTaskPins(root, guard, state, state.Generation, targets, true, workspacePinTestOwner, func(taskGuestIdentity) error { return nil }); err == nil {
		t.Fatal("released before durable discarded disposition")
	}
	writeWorkspacePinDisposition(t, targets[1].Identity.Path, state)
	if err := os.Remove(state.Guest.Path); err != nil {
		t.Fatal(err)
	}
	if err := updateWorkspaceTaskPins(root, guard, state, state.Generation, targets, true, workspacePinTestOwner, nil); err == nil {
		t.Fatal("released without quarantine confirmation")
	}
	if err := updateWorkspaceTaskPins(root, guard, state, state.Generation, targets, true, workspacePinTestOwner, func(taskGuestIdentity) error { return errors.New("quarantine remains") }); err == nil {
		t.Fatal("released uncertain quarantine")
	}
	pins, err := storagepins.Load(root)
	if err != nil || len(pins.TaskPins()) != 2 {
		t.Fatalf("pins %v, error %v", pins, err)
	}
	if err := updateWorkspaceTaskPins(root, guard, state, state.Generation, targets, true, workspacePinTestOwner, func(identity taskGuestIdentity) error {
		if identity != *state.Guest {
			t.Fatal("wrong deletion identity")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	pins, err = storagepins.Load(root)
	if err != nil || len(pins.TaskPins()) != 0 {
		t.Fatalf("pins %v, error %v", pins, err)
	}
}
