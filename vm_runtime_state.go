package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const vmRuntimeStateFile = "runtime.json"

type vmRuntimeState struct {
	State     string    `json:"state"`
	PID       int       `json:"pid"`
	UpdatedAt time.Time `json:"updated_at"`
}

func writeVMRuntimeState(vmDir, state string) error {
	state = strings.TrimSpace(state)
	if vmDir == "" || state == "" {
		return nil
	}
	rt := vmRuntimeState{
		State:     state,
		PID:       os.Getpid(),
		UpdatedAt: time.Now().UTC(),
	}
	data, err := json.MarshalIndent(rt, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal runtime state: %w", err)
	}
	path := filepath.Join(vmDir, vmRuntimeStateFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("write runtime state: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename runtime state: %w", err)
	}
	return nil
}

func readVMRuntimeState(vmDir string) (vmRuntimeState, error) {
	data, err := os.ReadFile(filepath.Join(vmDir, vmRuntimeStateFile))
	if err != nil {
		return vmRuntimeState{}, err
	}
	var rt vmRuntimeState
	if err := json.Unmarshal(data, &rt); err != nil {
		return vmRuntimeState{}, fmt.Errorf("parse runtime state: %w", err)
	}
	rt.State = canonicalVMState(rt.State)
	return rt, nil
}

func detectRuntimeState(vmDir string) string {
	rt, err := readVMRuntimeState(vmDir)
	if err != nil || rt.State == "" {
		return ""
	}
	if !isRunLockHeld(vmDir) {
		return ""
	}
	switch rt.State {
	case "starting", "running", "stopping", "paused", "suspended", "error":
		return rt.State
	default:
		return ""
	}
}

func isRunLockHeld(vmDir string) bool {
	lock, err := AcquireRunLock(vmDir)
	if err == nil {
		lock.Release()
		return false
	}
	return errors.Is(err, ErrRunLockHeld)
}

func noteVMRuntimeState(vmDir, state string) {
	if err := writeVMRuntimeState(vmDir, state); err != nil && verbose {
		fmt.Fprintf(os.Stderr, "warning: write runtime state: %v\n", err)
	}
}
