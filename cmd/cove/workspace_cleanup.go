package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

type workspaceCleanupGuard interface {
	Release() error
}

type workspaceCleanupDeps struct {
	CaptureRuntime func(taskDisposition, string) (workspaceRuntimeOwner, error)
	StoppedOwned   func(context.Context, string, workspaceRuntimeOwner) (bool, error)
	Identify       func(string) (taskGuestIdentity, error)
	VerifyOwner    func(taskDisposition, string) error
	// PinGuard excludes pin changes and guest directory renames through deletion.
	PinGuard    func(taskDisposition) (workspaceCleanupGuard, bool, error)
	Stop        func(context.Context, string) error
	Stopped     func(context.Context, string) (bool, error)
	Lock        func(string) (workspaceCleanupGuard, error)
	LiveRuntime func(string) (bool, error)
	DiskHolders func(string) ([]int, error)
	Delete      func(taskGuestIdentity) error
}

func cleanupWorkspaceGuest(ctx context.Context, state taskDisposition, generation string, d workspaceCleanupDeps) (err error) {
	if err := validateTaskDisposition(state, true); err != nil {
		return err
	}
	if generation != state.Generation {
		return fmt.Errorf("task owner generation differs")
	}
	if state.State != "stopping" || !state.Owned || !state.TaskSucceeded || state.Policy != "discard-success" {
		return fmt.Errorf("workspace cleanup requires a stopping owned successful task")
	}
	if d.CaptureRuntime == nil || d.Identify == nil || d.VerifyOwner == nil || d.PinGuard == nil || d.Stop == nil || d.Stopped == nil || d.Lock == nil || d.LiveRuntime == nil || d.DiskHolders == nil || d.Delete == nil {
		return fmt.Errorf("workspace cleanup requires coordinated pin and identity-bound deletion gates; guest retained")
	}
	if err := d.VerifyOwner(state, generation); err != nil {
		return fmt.Errorf("verify task cleanup owner: %w", err)
	}
	checkIdentities := func() error {
		for _, identity := range []*taskGuestIdentity{state.SourceGuest, state.Guest} {
			current, err := d.Identify(identity.Path)
			if err != nil {
				return fmt.Errorf("identify workspace cleanup directory: %w", err)
			}
			if current != *identity {
				return fmt.Errorf("workspace cleanup directory identity changed")
			}
		}
		return nil
	}
	if err := checkIdentities(); err != nil {
		return err
	}
	pins, pinned, err := d.PinGuard(state)
	if err != nil {
		if pins != nil {
			err = errors.Join(err, pins.Release())
		}
		return fmt.Errorf("guard workspace cleanup pins: %w", err)
	}
	if pins == nil {
		return fmt.Errorf("workspace cleanup pin guard unavailable")
	}
	defer func() { err = errors.Join(err, pins.Release()) }()
	if pinned {
		return fmt.Errorf("workspace guest or run is operator pinned; guest retained")
	}
	if err := checkIdentities(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	runtimeOwner, err := d.CaptureRuntime(state, generation)
	if err != nil {
		return fmt.Errorf("capture workspace runtime owner: %w", err)
	}
	if err := d.Stop(ctx, state.Guest.Path); err != nil {
		return fmt.Errorf("request workspace stop: %w", err)
	}
	var stopped bool
	if d.StoppedOwned != nil {
		stopped, err = d.StoppedOwned(ctx, state.Guest.Path, runtimeOwner)
	} else {
		stopped, err = d.Stopped(ctx, state.Guest.Path)
	}
	if err != nil {
		return fmt.Errorf("verify workspace stop: %w", err)
	}
	if !stopped {
		return fmt.Errorf("workspace stop is not positively verified; guest retained")
	}
	lock, err := d.Lock(state.Guest.Path)
	if err != nil {
		if lock != nil {
			err = errors.Join(err, lock.Release())
		}
		return fmt.Errorf("lock stopped workspace guest: %w", err)
	}
	if lock == nil {
		return fmt.Errorf("workspace cleanup run lock unavailable")
	}
	defer func() { err = errors.Join(err, lock.Release()) }()
	if err := checkIdentities(); err != nil {
		return err
	}
	if err := d.VerifyOwner(state, generation); err != nil {
		return fmt.Errorf("recheck task cleanup owner: %w", err)
	}
	live, err := d.LiveRuntime(state.Guest.Path)
	if err != nil {
		return fmt.Errorf("inspect workspace runtime owner: %w", err)
	}
	if live {
		return fmt.Errorf("workspace runtime still has a live owner; guest retained")
	}
	holders, err := d.DiskHolders(state.Guest.Path)
	if err != nil {
		return fmt.Errorf("inspect workspace disk holders: %w", err)
	}
	if len(holders) != 0 {
		return fmt.Errorf("workspace disk still has open holders; guest retained")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := checkIdentities(); err != nil {
		return err
	}
	return d.Delete(*state.Guest)
}

func defaultWorkspaceCleanupDeps() workspaceCleanupDeps {
	return workspaceCleanupDeps{
		Identify:       identifyTaskGuest,
		CaptureRuntime: captureWorkspaceRuntimeOwner,
		StoppedOwned:   waitWorkspaceRuntimeStopped,
		VerifyOwner: func(state taskDisposition, generation string) error {
			if state.OwnerPID != os.Getpid() {
				return fmt.Errorf("task cleanup owner differs from current process")
			}
			status, err := taskDispositionOwnerStatus(state, generation, func(pid int) (time.Time, bool, error) {
				started := processStartedAt(pid)
				if started.IsZero() {
					return time.Time{}, true, fmt.Errorf("task owner observation unavailable")
				}
				return started, true, nil
			})
			if err != nil {
				return err
			}
			if status != "active" {
				return fmt.Errorf("task cleanup owner is not positively verified")
			}
			return nil
		},
		Stop: func(ctx context.Context, dir string) error {
			client := NewControlClient(GetControlSocketPathForVM(dir))
			response, err := client.SendRequestCtx(ctx, &controlpb.ControlRequest{Type: "request-stop"})
			if err != nil {
				return err
			}
			if response == nil || !response.Success {
				return fmt.Errorf("owned guest stop failed")
			}
			return nil
		},
		Stopped: waitWorkspacePositivelyStopped,
		Lock:    func(dir string) (workspaceCleanupGuard, error) { return AcquireRunLock(dir) },
		LiveRuntime: func(dir string) (bool, error) {
			_, live, err := liveVMProcessForDirectory(dir, defaultVMProcessCollector())
			return live, err
		},
		DiskHolders: func(dir string) ([]int, error) { return openFileHolderPIDs(vmPrimaryDiskPath(dir)) },
	}
}

func waitWorkspacePositivelyStopped(ctx context.Context, dir string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		state, err := ctlVMStatusState(GetControlSocketPathForVM(dir), time.Second)
		if err != nil {
			return false, err
		}
		if state == "stopped" {
			return true, nil
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return false, ctx.Err()
		case <-timer.C:
		}
	}
}
