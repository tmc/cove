package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

func workspaceLifecycleObservation(vm, dir string) map[string]any {
	out := map[string]any{"vm": vm, "guest_directory": dir, "observed_at": time.Now().UTC().Format(time.RFC3339Nano), "owner_identity": "unverified", "pid_presence": "unknown", "runtime_metadata": "unavailable"}
	file, err := os.Open(filepath.Join(dir, "runtime.json"))
	if err != nil {
		return out
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	if err != nil || len(data) > 64<<10 {
		return out
	}
	var state struct {
		PID       int    `json:"pid"`
		State     string `json:"state"`
		UpdatedAt string `json:"updated_at"`
	}
	if json.Unmarshal(data, &state) != nil {
		return out
	}
	out["runtime_metadata"] = "present"
	out["reported_state"] = state.State
	out["reported_updated_at"] = state.UpdatedAt
	if state.PID <= 0 || state.PID > 1<<31-1 {
		return out
	}
	out["reported_pid"] = state.PID
	switch err := syscall.Kill(state.PID, 0); err {
	case nil:
		out["pid_presence"] = "present"
	case syscall.ESRCH:
		out["pid_presence"] = "absent"
	}
	return out
}
