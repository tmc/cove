package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

const workspaceRuntimeTailLimit = 64 << 10

type workspaceRuntimeTail struct {
	mu   sync.Mutex
	path string
	data []byte
	err  error
}

func (w *workspaceRuntimeTail) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := len(p)
	if len(p) >= workspaceRuntimeTailLimit {
		w.data = append(w.data[:0], p[len(p)-workspaceRuntimeTailLimit:]...)
	} else {
		if excess := len(w.data) + len(p) - workspaceRuntimeTailLimit; excess > 0 {
			copy(w.data, w.data[excess:])
			w.data = w.data[:len(w.data)-excess]
		}
		w.data = append(w.data, p...)
	}
	if err := writeWorkspaceRuntimeDiagnostic(w.path, w.data); err != nil {
		w.err = err
	}
	// A diagnostic write failure must not interrupt the guest owner.
	return n, nil
}

type workspaceRuntimeExit struct {
	PID              int    `json:"pid"`
	StartedAt        string `json:"started_at"`
	EndedAt          string `json:"ended_at"`
	ExitCode         int    `json:"exit_code"`
	Error            string `json:"error,omitempty"`
	DiagnosticsError string `json:"diagnostics_error,omitempty"`
}

func runWorkspaceRuntimeCommand(env commandEnv, _ string, args []string) int {
	if len(args) != 2 {
		return commandError(env, fmt.Errorf("workspace runtime requires VM name and directory"))
	}
	exe, err := os.Executable()
	if err != nil {
		return commandError(env, err)
	}
	dir, err := os.MkdirTemp(args[1], "workspace-runtime-")
	if err != nil {
		return commandError(env, fmt.Errorf("create runtime diagnostics: %w", err))
	}
	// This is an observation pointer, not authority to stop or remove a guest.
	writeWorkspaceRuntimeDiagnostic(filepath.Join(args[1], "workspace-runtime-diagnostics.json"), []byte(fmt.Sprintf("{\"directory\":%q}\n", filepath.Base(dir))))
	cmd := exec.Command(exe, "-vm", args[0], "-headless", "run")
	return commandError(env, monitorWorkspaceRuntime(cmd, dir))
}

func monitorWorkspaceRuntime(cmd *exec.Cmd, dir string) error {
	stdout := &workspaceRuntimeTail{path: filepath.Join(dir, "stdout-tail.log")}
	stderr := &workspaceRuntimeTail{path: filepath.Join(dir, "stderr-tail.log")}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	state := workspaceRuntimeExit{StartedAt: time.Now().UTC().Format(time.RFC3339Nano), ExitCode: -1}
	err := cmd.Start()
	if err == nil {
		state.PID = cmd.Process.Pid
		err = cmd.Wait()
		state.ExitCode = cmd.ProcessState.ExitCode()
	}
	state.EndedAt = time.Now().UTC().Format(time.RFC3339Nano)
	if err != nil {
		state.Error = err.Error()
	}
	for _, tail := range []*workspaceRuntimeTail{stdout, stderr} {
		if tail.err != nil {
			state.DiagnosticsError = tail.err.Error()
		}
	}
	data, marshalErr := json.MarshalIndent(state, "", "  ")
	if marshalErr != nil {
		return marshalErr
	}
	if writeErr := writeWorkspaceRuntimeDiagnostic(filepath.Join(dir, "exit.json"), append(data, '\n')); writeErr != nil {
		return fmt.Errorf("record runtime exit: %w", writeErr)
	}
	return err
}

func writeWorkspaceRuntimeDiagnostic(path string, data []byte) error {
	tmp, err := atomicWriteFile(path, data, 0600)
	if tmp != "" {
		os.Remove(tmp)
	}
	return err
}
