package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tmc/cove/internal/mutationguard"
	"github.com/tmc/cove/internal/vmconfig"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

const workspaceRuntimeTailLimit = 64 << 10

type workspaceRuntimeTail struct {
	mu    sync.Mutex
	root  *os.Root
	name  string
	data  []byte
	err   error
	crash *workspaceRuntimeCrash
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
	if err := writeWorkspaceRuntimeDiagnostic(w.root, w.name, w.data); err != nil {
		w.err = err
	}
	if w.crash != nil && w.crash.append(p) {
		if err := writeWorkspaceRuntimeDiagnostic(w.root, "stderr-crash-context.log", w.crash.context); err != nil {
			w.err = err
		}
	}
	// A diagnostic write failure must not interrupt the guest owner.
	return n, nil
}

type workspaceRuntimeExit struct {
	Generation       string `json:"generation"`
	PID              int    `json:"pid"`
	StartedAt        string `json:"started_at"`
	EndedAt          string `json:"ended_at"`
	ExitCode         int    `json:"exit_code"`
	Error            string `json:"error,omitempty"`
	DiagnosticsError string `json:"diagnostics_error,omitempty"`
}

func runWorkspaceRuntimeCommand(env commandEnv, _ string, args []string) int {
	if len(args) != 3 || !validWorkspaceRuntimeGeneration(args[2]) {
		return commandError(env, fmt.Errorf("workspace runtime requires VM name, directory and generation"))
	}
	exe, err := os.Executable()
	if err != nil {
		return commandError(env, err)
	}
	guest, diagnostic, name, err := admitWorkspaceRuntimeDiagnostics(coveRoot(), args[0], args[1])
	if err != nil {
		return commandError(env, err)
	}
	defer guest.Close()
	defer diagnostic.Close()
	// This pointer is observation only; receipts provide generation authority.
	pointerErr := writeWorkspaceRuntimeDiagnostic(guest, "workspace-runtime-diagnostics.json", []byte(fmt.Sprintf("{\"directory\":%q}\n", name)))
	cmd := exec.Command(exe, workspaceRuntimeArgs(args[0])...)
	return commandError(env, monitorWorkspaceRuntimeRoot(cmd, diagnostic, args[2], pointerErr))
}

func workspaceRuntimeArgs(vm string) []string {
	return []string{"-vm", vm, "-headless", "-auto-mount-shared-folders=false", "run"}
}

func monitorWorkspaceRuntime(cmd *exec.Cmd, dir, generation string) error {
	if !validWorkspaceRuntimeGeneration(generation) {
		return fmt.Errorf("invalid runtime generation")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	return monitorWorkspaceRuntimeRoot(cmd, root, generation, nil)
}

func monitorWorkspaceRuntimeRoot(cmd *exec.Cmd, root *os.Root, generation string, diagnosticErr error) error {
	if !validWorkspaceRuntimeGeneration(generation) {
		return fmt.Errorf("invalid runtime generation")
	}
	stdout := &workspaceRuntimeTail{root: root, name: "stdout-tail.log"}
	stderr := &workspaceRuntimeTail{root: root, name: "stderr-tail.log", crash: new(workspaceRuntimeCrash)}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	state := workspaceRuntimeExit{Generation: generation, StartedAt: time.Now().UTC().Format(time.RFC3339Nano), ExitCode: -1}
	if diagnosticErr != nil {
		state.DiagnosticsError = diagnosticErr.Error()
	}
	err := cmd.Start()
	if err == nil {
		state.PID = cmd.Process.Pid
		started := processStartedAt(state.PID)
		if started.IsZero() {
			state.DiagnosticsError = "runtime process start observation unavailable"
		} else {
			state.StartedAt = started.UTC().Format(time.RFC3339Nano)
			owner := workspaceRuntimeOwner{Generation: generation, PID: state.PID, StartedAt: state.StartedAt}
			data, _ := json.Marshal(owner)
			if writeErr := writeWorkspaceRuntimeDiagnostic(root, "running.json", data); writeErr != nil {
				state.DiagnosticsError = writeErr.Error()
			}
		}
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
	if writeErr := writeWorkspaceRuntimeDiagnostic(root, "exit.json", append(data, '\n')); writeErr != nil {
		return fmt.Errorf("record runtime exit: %w", writeErr)
	}
	return err
}

func writeWorkspaceRuntimeDiagnostic(root *os.Root, name string, data []byte) error {
	if root == nil || filepath.Base(name) != name || name == "." || name == "" {
		return fmt.Errorf("invalid diagnostic target")
	}
	id, err := generateRunID()
	if err != nil {
		return err
	}
	temporary := "." + name + "-" + id
	file, err := root.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(temporary)
	_, writeErr := file.Write(data)
	if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
		return err
	}
	if err := root.Rename(temporary, name); err != nil {
		return err
	}
	return syncWorkspaceRoot(root)
}

func admitWorkspaceRuntimeDiagnostics(storageRoot, name, path string) (*os.Root, *os.Root, string, error) {
	guard, err := mutationguard.Acquire(storageRoot)
	if err != nil {
		return nil, nil, "", err
	}
	defer guard.Release()
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || canonical != path {
		return nil, nil, "", fmt.Errorf("runtime guest path must be canonical and existing")
	}
	resolved, exists := vmconfig.ExistingPath(name)
	if !exists || resolved != path {
		return nil, nil, "", fmt.Errorf("runtime guest name identity differs")
	}
	identity, err := identifyTaskGuest(path)
	if err != nil {
		return nil, nil, "", err
	}
	guest, err := os.OpenRoot(path)
	if err != nil {
		return nil, nil, "", err
	}
	fail := func(err error) (*os.Root, *os.Root, string, error) { guest.Close(); return nil, nil, "", err }
	if err := checkWorkspaceRootIdentity(guest, ".", identity); err != nil {
		return fail(err)
	}
	id, err := generateRunID()
	if err != nil {
		return fail(err)
	}
	child := "workspace-runtime-" + id
	if err := guest.Mkdir(child, 0700); err != nil {
		return fail(err)
	}
	diagnostic, err := guest.OpenRoot(child)
	if err != nil {
		return fail(err)
	}
	current, err := identifyTaskGuest(path)
	if err != nil || current != identity {
		diagnostic.Close()
		return fail(fmt.Errorf("runtime guest admission identity changed"))
	}
	return guest, diagnostic, child, nil
}
