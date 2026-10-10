package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tmc/cove/internal/storagepins"
	"github.com/tmc/cove/internal/vmconfig"
)

func TestWorkspaceProductionDiscardAndFinalize(t *testing.T) {
	for _, tt := range []struct {
		name     string
		operator bool
		other    bool
		manual   bool
	}{{"discard", false, false, false}, {"operator", true, false, false}, {"other-generation", false, true, false}, {"manual-failed", false, false, true}, {"manual-operator", true, false, true}, {"manual-other-generation", false, true, true}} {
		t.Run(tt.name, func(t *testing.T) {
			root, guard, state, targets := workspacePinFixture(t)
			t.Setenv(vmconfig.StateDirEnv, root)
			state.OwnerStartedAt = processStartedAt(os.Getpid()).UTC().Format(time.RFC3339Nano)
			state.State = "stopping"
			state.TaskSucceeded = true
			if tt.manual {
				state.TaskSucceeded = false
				state.Policy = "retain"
				state.OwnerPID = exitedDiscardPID(t)
				state.DiscardRequest = &taskDiscardRequest{PID: os.Getpid(), StartedAt: processStartedAt(os.Getpid()).UTC().Format(time.RFC3339Nano), RequestedAt: time.Now().UTC().Format(time.RFC3339Nano)}
			}
			writeWorkspacePinDisposition(t, targets[1].Identity.Path, state)
			if err := updateWorkspaceTaskPins(root, guard, state, state.Generation, targets, false, workspacePinTestOwner, nil); err != nil {
				t.Fatal(err)
			}
			if tt.operator || tt.other {
				if err := storagepins.UpdateWithGuard(root, guard, func(pins *storagepins.File) (bool, error) {
					if tt.operator {
						pins.Add("vm", "workspace", time.Now())
						return true, nil
					}
					pin := pins.TaskPins()[0]
					pin.Owner.Generation = "ffffffffffffffffffffffffffffffff"
					return true, pins.AddTask(pin)
				}); err != nil {
					t.Fatal(err)
				}
			}
			alias := filepath.Join(root, "vms", "workspace")
			if err := os.Symlink("workspace.covevm", alias); err != nil {
				t.Fatal(err)
			}
			guard.Release()
			d := productionWorkspaceCleanupDeps()
			// Runtime observations are modeled; filesystem/pin/quarantine operations are real fixtures.
			d.CaptureRuntime = func(taskDisposition, string) (workspaceRuntimeOwner, error) { return workspaceRuntimeOwner{}, nil }
			d.Stop = func(context.Context, string) error { return nil }
			d.StoppedOwned = func(context.Context, string, workspaceRuntimeOwner) (bool, error) { return true, nil }
			d.LiveRuntime = func(string) (bool, error) { return false, nil }
			d.DiskHolders = func(string) ([]int, error) { return nil, nil }
			err := cleanupWorkspaceGuest(context.Background(), state, state.Generation, d)
			if tt.operator || tt.other {
				if err == nil {
					t.Fatal("deleted pinned guest")
				}
				if _, err := os.Stat(state.Guest.Path); err != nil {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := finalizeDiscardedWorkspaceGuest(state, state.Generation); err == nil {
				t.Fatal("released before durable discarded")
			}
			pins, err := storagepins.Load(root)
			if err != nil || len(pins.TaskPins()) != 2 {
				t.Fatalf("pins prematurely removed %v", err)
			}
			state.State = "discarded"
			writeWorkspacePinDisposition(t, targets[1].Identity.Path, state)
			if tt.manual {
				changed := state
				request := *state.DiscardRequest
				request.RequestedAt = time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano)
				changed.DiscardRequest = &request
				writeWorkspacePinDisposition(t, targets[1].Identity.Path, changed)
				if err := finalizeDiscardedWorkspaceGuest(changed, changed.Generation); err == nil {
					t.Fatal("accepted unrelated quarantine operator request")
				}
				writeWorkspacePinDisposition(t, targets[1].Identity.Path, state)
			}
			if err := finalizeDiscardedWorkspaceGuest(state, state.Generation); err != nil {
				t.Fatal(err)
			}
			pins, err = storagepins.Load(root)
			if err != nil || len(pins.TaskPins()) != 0 {
				t.Fatalf("pins remain %v", err)
			}
			if _, err := os.Lstat(alias); !os.IsNotExist(err) {
				t.Fatalf("alias remains %v", err)
			}
			if _, err := os.Stat(state.Source); err != nil {
				t.Fatalf("source changed %v", err)
			}
		})
	}
}

func TestWorkspaceHolderInventoryIncludesAuxAndAttachedFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"disk.img", "aux.img", "attached.raw", "run.lock"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	pids, err := workspaceGuestFileHoldersWith(dir, func(path string) ([]int, error) { seen[filepath.Base(path)] = true; return []int{123}, nil })
	if err != nil || len(pids) != 1 || !seen["aux.img"] || !seen["attached.raw"] || seen["run.lock"] {
		t.Fatalf("inventory=%v pids=%v error=%v", seen, pids, err)
	}
	if err := os.Symlink("disk.img", filepath.Join(dir, "linked.raw")); err != nil {
		t.Fatal(err)
	}
	if _, err := workspaceGuestFileHoldersWith(dir, func(string) ([]int, error) { return nil, nil }); err == nil {
		t.Fatal("accepted ambiguous linked attachment")
	}
}

func TestWorkspaceHolderInventoryBounds(t *testing.T) {
	for _, tt := range []struct {
		name  string
		depth bool
	}{{"entries", false}, {"depth", true}} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.depth {
				path := dir
				for i := 0; i < 33; i++ {
					path = filepath.Join(path, "nested")
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				for i := 0; i < 4097; i++ {
					if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file-%04d", i)), nil, 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			calls := 0
			if _, err := workspaceGuestFileHoldersWith(dir, func(string) ([]int, error) { calls++; return nil, nil }); err == nil {
				t.Fatal("accepted unbounded inventory")
			}
			if calls > 4096 {
				t.Fatalf("unbounded holder calls: %d", calls)
			}
		})
	}
}
