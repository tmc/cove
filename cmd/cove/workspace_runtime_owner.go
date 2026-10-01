package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

type workspaceRuntimeOwner struct {
	Generation string `json:"generation"`
	PID        int    `json:"pid"`
	StartedAt  string `json:"started_at"`
	Directory  string `json:"-"`
}

func validWorkspaceRuntimeGeneration(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func readWorkspaceRuntimeReceipt(path string, dst any) error {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 8192 {
		return fmt.Errorf("invalid runtime receipt size or type")
	}
	dec := json.NewDecoder(io.LimitReader(f, 8193))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("trailing runtime receipt data")
	}
	return nil
}

func captureWorkspaceRuntimeOwner(state taskDisposition, generation string) (workspaceRuntimeOwner, error) {
	if state.Guest == nil || generation != state.Generation || !validWorkspaceRuntimeGeneration(generation) {
		return workspaceRuntimeOwner{}, fmt.Errorf("invalid runtime owner authority")
	}
	identity, err := identifyTaskGuest(state.Guest.Path)
	if err != nil || identity != *state.Guest {
		return workspaceRuntimeOwner{}, fmt.Errorf("runtime guest identity differs")
	}
	var pointer struct {
		Directory string `json:"directory"`
	}
	if err := readWorkspaceRuntimeReceipt(filepath.Join(identity.Path, "workspace-runtime-diagnostics.json"), &pointer); err != nil {
		return workspaceRuntimeOwner{}, err
	}
	if pointer.Directory == "." || filepath.Base(pointer.Directory) != pointer.Directory || pointer.Directory == "" {
		return workspaceRuntimeOwner{}, fmt.Errorf("invalid runtime diagnostic directory")
	}
	dir := filepath.Join(identity.Path, pointer.Directory)
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return workspaceRuntimeOwner{}, fmt.Errorf("invalid runtime diagnostic directory")
	}
	var owner workspaceRuntimeOwner
	if err := readWorkspaceRuntimeReceipt(filepath.Join(dir, "running.json"), &owner); err != nil {
		return owner, err
	}
	if owner.Generation != generation || owner.PID <= 0 {
		return owner, fmt.Errorf("runtime owner generation differs")
	}
	expected, err := time.Parse(time.RFC3339Nano, owner.StartedAt)
	if err != nil || expected.IsZero() || !processStartedAt(owner.PID).Equal(expected) {
		return owner, fmt.Errorf("runtime owner process differs or unavailable")
	}
	owner.Directory = dir
	return owner, nil
}

func workspaceRuntimeExited(owner workspaceRuntimeOwner) bool {
	var exit workspaceRuntimeExit
	if readWorkspaceRuntimeReceipt(filepath.Join(owner.Directory, "exit.json"), &exit) != nil {
		return false
	}
	ended, err := time.Parse(time.RFC3339Nano, exit.EndedAt)
	started, startErr := time.Parse(time.RFC3339Nano, owner.StartedAt)
	return validWorkspaceRuntimeGeneration(owner.Generation) && owner.PID > 0 && exit.Generation == owner.Generation && exit.PID == owner.PID && exit.StartedAt == owner.StartedAt && exit.ExitCode == 0 && exit.Error == "" && err == nil && startErr == nil && !ended.Before(started)
}

func waitWorkspaceRuntimeStopped(ctx context.Context, dir string, owner workspaceRuntimeOwner) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		state, _ := ctlVMStatusState(GetControlSocketPathForVM(dir), time.Second)
		if state == "stopped" || workspaceRuntimeExited(owner) {
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
