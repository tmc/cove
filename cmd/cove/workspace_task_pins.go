package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tmc/cove/internal/mutationguard"
	"github.com/tmc/cove/internal/storagepins"
	"github.com/tmc/cove/internal/vmconfig"
)

type workspaceTaskPinTarget struct {
	Category string
	ID       string
	Identity taskGuestIdentity
}

func addWorkspaceTaskPins(root string, state taskDisposition, generation string, targets []workspaceTaskPinTarget) error {
	guard, err := mutationguard.Acquire(root)
	if err != nil {
		return err
	}
	defer guard.Release()
	return addWorkspaceTaskPinsWithGuard(root, guard, state, generation, targets)
}

func addWorkspaceTaskPinsWithGuard(root string, guard *mutationguard.Guard, state taskDisposition, generation string, targets []workspaceTaskPinTarget) error {
	return updateWorkspaceTaskPins(root, guard, state, generation, targets, false, defaultWorkspaceCleanupDeps().VerifyOwner, nil)
}

func releaseWorkspaceTaskPinsWithGuard(root string, guard *mutationguard.Guard, state taskDisposition, generation string, targets []workspaceTaskPinTarget, confirmDeleted func(taskGuestIdentity) error) error {
	return updateWorkspaceTaskPins(root, guard, state, generation, targets, true, defaultWorkspaceCleanupDeps().VerifyOwner, confirmDeleted)
}

func updateWorkspaceTaskPins(root string, guard *mutationguard.Guard, state taskDisposition, generation string, targets []workspaceTaskPinTarget, release bool, verify func(taskDisposition, string) error, confirmDeleted func(taskGuestIdentity) error) error {
	if err := guard.Check(root); err != nil {
		return err
	}
	if err := validateTaskDisposition(state, true); err != nil {
		return err
	}
	if generation != state.Generation {
		return fmt.Errorf("task pin owner generation differs")
	}
	if !state.Owned || state.Guest == nil {
		return fmt.Errorf("task pins require a recorded owned guest")
	}
	if verify == nil {
		return fmt.Errorf("task pin owner verification unavailable")
	}
	if err := verify(state, generation); err != nil {
		return err
	}
	if _, _, err := storagepins.ParseRef("run:" + state.RunID); err != nil {
		return err
	}
	runPath, err := filepath.Abs(filepath.Join(root, "runs", state.RunID))
	if err != nil {
		return err
	}
	if len(targets) != 2 {
		return fmt.Errorf("task pins require exactly the recorded guest and run")
	}
	seen := map[string]bool{}
	for _, target := range targets {
		if seen[target.Category] {
			return fmt.Errorf("duplicate task pin target")
		}
		seen[target.Category] = true
		switch target.Category {
		case "vm":
			if target.ID != vmconfig.NameForPath(state.Guest.Path) || target.Identity != *state.Guest {
				return fmt.Errorf("task VM pin differs from recorded guest")
			}
			if !release {
				current, err := identifyTaskGuest(target.Identity.Path)
				if err != nil || current != target.Identity {
					return fmt.Errorf("task VM pin directory identity unavailable or changed")
				}
			}
		case "run":
			if target.ID != state.RunID || target.Identity.Path != runPath {
				return fmt.Errorf("task run pin differs from canonical run bundle")
			}
			current, err := identifyTaskGuest(runPath)
			if err != nil || current != target.Identity {
				return fmt.Errorf("task run pin directory identity unavailable or changed")
			}
		default:
			return fmt.Errorf("unsupported task pin target")
		}
	}
	durable, err := readTaskDisposition(runPath)
	if err != nil {
		return fmt.Errorf("read durable task pin authority: %w", err)
	}
	if durable.RunID != state.RunID || durable.AttemptID != state.AttemptID || durable.Generation != state.Generation || durable.OwnerPID != state.OwnerPID || durable.OwnerStartedAt != state.OwnerStartedAt || durable.State != state.State || durable.Owned != state.Owned || durable.TaskSucceeded != state.TaskSucceeded || durable.Policy != state.Policy || durable.Source != state.Source || durable.SourceGuest == nil || *durable.SourceGuest != *state.SourceGuest || durable.Guest == nil || *durable.Guest != *state.Guest {
		return fmt.Errorf("task pin authority differs from durable disposition")
	}
	if release {
		if state.State != "discarded" || !state.TaskSucceeded || confirmDeleted == nil {
			return fmt.Errorf("task pin release requires durable discarded disposition and quarantine deletion confirmation")
		}
		if _, err := os.Lstat(state.Guest.Path); !os.IsNotExist(err) {
			return fmt.Errorf("task guest path remains or cannot be verified absent")
		}
		if err := confirmDeleted(*state.Guest); err != nil {
			return fmt.Errorf("confirm task quarantine deletion: %w", err)
		}
	} else if state.State == "discarded" {
		return fmt.Errorf("cannot pin a discarded guest")
	}
	owner := storagepins.TaskOwner{RunID: state.RunID, AttemptID: state.AttemptID, Generation: state.Generation, PID: state.OwnerPID, StartedAt: state.OwnerStartedAt}
	return storagepins.UpdateWithGuard(root, guard, func(pins *storagepins.File) (bool, error) {
		for _, target := range targets {
			identity := storagepins.DirectoryIdentity{Path: target.Identity.Path, Device: target.Identity.Device, Inode: target.Identity.Inode}
			if release {
				if _, err := pins.RemoveTask(target.Category, target.ID, owner, identity); err != nil {
					return false, err
				}
			} else {
				if err := pins.AddTask(storagepins.TaskPin{Category: target.Category, ID: target.ID, AddedAt: time.Now().UTC(), Owner: owner, Identity: identity}); err != nil {
					return false, err
				}
			}
		}
		return true, nil
	})
}
