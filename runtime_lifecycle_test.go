package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func TestRunCurrentVMWithDisposableClone(t *testing.T) {
	oldVMName := vmName
	oldVMDir := vmDir
	oldDisposableMode := disposableMode
	oldLinuxMode := linuxMode
	oldSetupHook := setupDisposableCloneHook
	oldCleanupHook := cleanupDisposableCloneHook
	oldRunMacHook := runMacOSVMHook
	oldRunLinuxHook := runLinuxVMHook
	t.Cleanup(func() {
		vmName = oldVMName
		vmDir = oldVMDir
		disposableMode = oldDisposableMode
		linuxMode = oldLinuxMode
		setupDisposableCloneHook = oldSetupHook
		cleanupDisposableCloneHook = oldCleanupHook
		runMacOSVMHook = oldRunMacHook
		runLinuxVMHook = oldRunLinuxHook
	})

	disposableMode = true
	linuxMode = false
	vmName = "research-base"
	vmDir = "/tmp/research-base"

	clone := DisposableClone{
		Name:   "research-base-d-20260330-120000",
		Path:   "/tmp/research-base-d-20260330-120000",
		Source: "research-base",
	}
	var gotCleanupPath string
	var gotRunVMName string
	var gotRunVMDir string

	setupDisposableCloneHook = func(opts DisposableSetupOptions) (DisposableClone, error) {
		if opts.Source != "research-base" || !opts.Linked || opts.CopyMachineID {
			t.Fatalf("SetupDisposableClone opts = %#v", opts)
		}
		return clone, nil
	}
	runMacOSVMHook = func() error {
		gotRunVMName = vmName
		gotRunVMDir = vmDir
		return nil
	}
	runLinuxVMHook = func() error {
		t.Fatal("runLinuxVMHook should not be called")
		return nil
	}
	cleanupDisposableCloneHook = func(path string) error {
		gotCleanupPath = path
		return nil
	}

	out, err := captureStdoutResult(t, runCurrentVM)
	if err != nil {
		t.Fatalf("runCurrentVM() error = %v", err)
	}
	if gotRunVMName != clone.Name {
		t.Fatalf("runCurrentVM() ran vmName %q, want %q", gotRunVMName, clone.Name)
	}
	if gotRunVMDir != clone.Path {
		t.Fatalf("runCurrentVM() ran vmDir %q, want %q", gotRunVMDir, clone.Path)
	}
	if gotCleanupPath != clone.Path {
		t.Fatalf("cleanup path = %q, want %q", gotCleanupPath, clone.Path)
	}
	if vmName != "research-base" {
		t.Fatalf("vmName after run = %q, want %q", vmName, "research-base")
	}
	if vmDir != "/tmp/research-base" {
		t.Fatalf("vmDir after run = %q, want %q", vmDir, "/tmp/research-base")
	}
	for _, want := range []string{
		"Disposable clone: " + clone.Name,
		"Disposable path: " + clone.Path,
		"Disposable clone removed: " + clone.Name,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("run output %q does not contain %q", out, want)
		}
	}
}

func TestRunCurrentVMCleansUpDisposableCloneAfterError(t *testing.T) {
	oldVMName := vmName
	oldVMDir := vmDir
	oldDisposableMode := disposableMode
	oldLinuxMode := linuxMode
	oldSetupHook := setupDisposableCloneHook
	oldCleanupHook := cleanupDisposableCloneHook
	oldRunMacHook := runMacOSVMHook
	t.Cleanup(func() {
		vmName = oldVMName
		vmDir = oldVMDir
		disposableMode = oldDisposableMode
		linuxMode = oldLinuxMode
		setupDisposableCloneHook = oldSetupHook
		cleanupDisposableCloneHook = oldCleanupHook
		runMacOSVMHook = oldRunMacHook
	})

	disposableMode = true
	linuxMode = false
	vmName = "research-base"
	vmDir = "/tmp/research-base"

	clone := DisposableClone{
		Name: "research-base-d-20260330-120000",
		Path: "/tmp/research-base-d-20260330-120000",
	}
	wantErr := errors.New("boom")
	cleanupCalled := false

	setupDisposableCloneHook = func(DisposableSetupOptions) (DisposableClone, error) {
		return clone, nil
	}
	runMacOSVMHook = func() error {
		return wantErr
	}
	cleanupDisposableCloneHook = func(path string) error {
		cleanupCalled = true
		if path != clone.Path {
			t.Fatalf("cleanup path = %q, want %q", path, clone.Path)
		}
		return nil
	}

	_, err := captureStdoutResult(t, runCurrentVM)
	if !errors.Is(err, wantErr) {
		t.Fatalf("runCurrentVM() error = %v, want %v", err, wantErr)
	}
	if !cleanupCalled {
		t.Fatal("cleanupDisposableCloneHook was not called")
	}
	if vmName != "research-base" {
		t.Fatalf("vmName after run = %q, want %q", vmName, "research-base")
	}
	if vmDir != "/tmp/research-base" {
		t.Fatalf("vmDir after run = %q, want %q", vmDir, "/tmp/research-base")
	}
}

func TestControlRuntimeInfrastructureHooks(t *testing.T) {
	oldStartFileHandle := startPreparedFileHandleNetworkHook
	oldStopFileHandle := stopPreparedFileHandleNetworkHook
	oldStartProxy := configureRequestedProxyAfterBootHook
	oldStopProxy := teardownRequestedProxyHook
	t.Cleanup(func() {
		startPreparedFileHandleNetworkHook = oldStartFileHandle
		stopPreparedFileHandleNetworkHook = oldStopFileHandle
		configureRequestedProxyAfterBootHook = oldStartProxy
		teardownRequestedProxyHook = oldStopProxy
	})

	var calls []string
	startPreparedFileHandleNetworkHook = func() {
		calls = append(calls, "start-filehandle")
	}
	stopPreparedFileHandleNetworkHook = func() {
		calls = append(calls, "stop-filehandle")
	}
	configureRequestedProxyAfterBootHook = func(cs *ControlServer) {
		if cs == nil {
			t.Fatal("configureRequestedProxyAfterBootHook received nil control server")
		}
		calls = append(calls, "start-proxy")
	}
	teardownRequestedProxyHook = func(cs *ControlServer) {
		if cs == nil {
			t.Fatal("teardownRequestedProxyHook received nil control server")
		}
		calls = append(calls, "stop-proxy")
	}

	controlServer := NewControlServerWithVMDir("", t.TempDir())
	startControlRuntimeInfrastructure(controlServer)
	stopControlRuntimeInfrastructure(controlServer)

	want := []string{"start-filehandle", "start-proxy", "stop-proxy", "stop-filehandle"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("runtime infrastructure calls = %v, want %v", calls, want)
	}
}

func captureStdoutResult(t *testing.T, fn func() error) (string, error) {
	t.Helper()

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	os.Stdout = w
	defer func() {
		os.Stdout = oldStdout
	}()

	done := make(chan error, 1)
	go func() {
		done <- fn()
		_ = w.Close()
	}()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	return buf.String(), <-done
}
