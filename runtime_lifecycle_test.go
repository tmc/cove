package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

func TestRunCurrentVMWithDisposableClone(t *testing.T) {
	stubAcquireRunLockHook(t)
	oldVMName := vmName
	oldVMDir := vmDir
	oldDisposableMode := disposableMode
	oldDisposableSourceDiskPath := disposableSourceDiskPath
	oldLinuxMode := linuxMode
	oldRuntimeSystemDiskPathOverride := runtimeSystemDiskPathOverride
	oldRuntimeSystemDiskAttachment := runtimeSystemDiskAttachment
	oldSetupHook := setupDisposableCloneHook
	oldCleanupHook := cleanupDisposableCloneHook
	oldRunMacHook := runMacOSVMHook
	oldRunLinuxHook := runLinuxVMHook
	t.Cleanup(func() {
		vmName = oldVMName
		vmDir = oldVMDir
		disposableMode = oldDisposableMode
		disposableSourceDiskPath = oldDisposableSourceDiskPath
		linuxMode = oldLinuxMode
		runtimeSystemDiskPathOverride = oldRuntimeSystemDiskPathOverride
		runtimeSystemDiskAttachment = oldRuntimeSystemDiskAttachment
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
	stubAcquireRunLockHook(t)
	oldVMName := vmName
	oldVMDir := vmDir
	oldDisposableMode := disposableMode
	oldDisposableSourceDiskPath := disposableSourceDiskPath
	oldLinuxMode := linuxMode
	oldRuntimeSystemDiskPathOverride := runtimeSystemDiskPathOverride
	oldRuntimeSystemDiskAttachment := runtimeSystemDiskAttachment
	oldSetupHook := setupDisposableCloneHook
	oldCleanupHook := cleanupDisposableCloneHook
	oldRunMacHook := runMacOSVMHook
	t.Cleanup(func() {
		vmName = oldVMName
		vmDir = oldVMDir
		disposableMode = oldDisposableMode
		disposableSourceDiskPath = oldDisposableSourceDiskPath
		linuxMode = oldLinuxMode
		runtimeSystemDiskPathOverride = oldRuntimeSystemDiskPathOverride
		runtimeSystemDiskAttachment = oldRuntimeSystemDiskAttachment
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

func TestRunCurrentVMWithRollbackSnapshotClone(t *testing.T) {
	stubAcquireRunLockHook(t)
	oldVMName := vmName
	oldVMDir := vmDir
	oldDisposableMode := disposableMode
	oldRollbackSnapshotName := rollbackSnapshotName
	oldDisposableSourceDiskPath := disposableSourceDiskPath
	oldLinuxMode := linuxMode
	oldRuntimeSystemDiskPathOverride := runtimeSystemDiskPathOverride
	oldRuntimeSystemDiskAttachment := runtimeSystemDiskAttachment
	oldSetupRollbackHook := setupRollbackSnapshotCloneHook
	oldCleanupHook := cleanupDisposableCloneHook
	oldRunMacHook := runMacOSVMHook
	oldRunLinuxHook := runLinuxVMHook
	t.Cleanup(func() {
		vmName = oldVMName
		vmDir = oldVMDir
		disposableMode = oldDisposableMode
		rollbackSnapshotName = oldRollbackSnapshotName
		disposableSourceDiskPath = oldDisposableSourceDiskPath
		linuxMode = oldLinuxMode
		runtimeSystemDiskPathOverride = oldRuntimeSystemDiskPathOverride
		runtimeSystemDiskAttachment = oldRuntimeSystemDiskAttachment
		setupRollbackSnapshotCloneHook = oldSetupRollbackHook
		cleanupDisposableCloneHook = oldCleanupHook
		runMacOSVMHook = oldRunMacHook
		runLinuxVMHook = oldRunLinuxHook
	})

	disposableMode = false
	rollbackSnapshotName = "clean-base"
	linuxMode = false
	vmName = "research-base"
	vmDir = "/tmp/research-base"

	clone := DisposableClone{
		Name:   "research-base-d-20260422-123456",
		Path:   "/tmp/research-base-d-20260422-123456",
		Source: "research-base",
	}
	var gotCleanupPath string
	var gotRunVMName string
	var gotRunVMDir string

	setupRollbackSnapshotCloneHook = func(opts RollbackSnapshotCloneOptions) (DisposableClone, error) {
		if opts.Source != "research-base" || opts.Snapshot != "clean-base" {
			t.Fatalf("SetupRollbackSnapshotClone opts = %#v", opts)
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
		"Rollback snapshot: clean-base",
		"Rollback clone: " + clone.Name,
		"Rollback path: " + clone.Path,
		"Rollback clone removed: " + clone.Name,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("run output %q does not contain %q", out, want)
		}
	}
}

func TestRunCurrentVMWithTemporaryRAMSystemDiskAttachment(t *testing.T) {
	stubAcquireRunLockHook(t)
	oldVMName := vmName
	oldVMDir := vmDir
	oldDisposableMode := disposableMode
	oldDisposableSourceDiskPath := disposableSourceDiskPath
	oldLinuxMode := linuxMode
	oldRuntimeSystemDiskPathOverride := runtimeSystemDiskPathOverride
	oldRuntimeSystemDiskAttachment := runtimeSystemDiskAttachment
	oldSetupHook := setupDisposableCloneHook
	oldCleanupHook := cleanupDisposableCloneHook
	oldRunMacHook := runMacOSVMHook
	t.Cleanup(func() {
		vmName = oldVMName
		vmDir = oldVMDir
		disposableMode = oldDisposableMode
		disposableSourceDiskPath = oldDisposableSourceDiskPath
		linuxMode = oldLinuxMode
		runtimeSystemDiskPathOverride = oldRuntimeSystemDiskPathOverride
		runtimeSystemDiskAttachment = oldRuntimeSystemDiskAttachment
		setupDisposableCloneHook = oldSetupHook
		cleanupDisposableCloneHook = oldCleanupHook
		runMacOSVMHook = oldRunMacHook
	})

	disposableMode = true
	disposableSourceDiskPath = "/tmp/checkpoint/disk.img"
	runtimeSystemDiskAttachment = systemDiskAttachmentTemporaryRAM
	linuxMode = false
	vmName = "research-base"
	vmDir = "/tmp/research-base"

	clone := DisposableClone{
		Name: "research-base-d-20260422-123456",
		Path: "/tmp/research-base-d-20260422-123456",
	}
	var gotRunVMName string
	var gotRunVMDir string
	var gotDiskPathOverride string
	var gotAttachmentMode systemDiskAttachmentMode

	setupDisposableCloneHook = func(opts DisposableSetupOptions) (DisposableClone, error) {
		if opts.SourceDiskPath != "/tmp/checkpoint/disk.img" {
			t.Fatalf("SetupDisposableClone opts.SourceDiskPath = %q", opts.SourceDiskPath)
		}
		return clone, nil
	}
	runMacOSVMHook = func() error {
		gotRunVMName = vmName
		gotRunVMDir = vmDir
		gotDiskPathOverride = runtimeSystemDiskPathOverride
		gotAttachmentMode = runtimeSystemDiskAttachment
		return nil
	}
	cleanupDisposableCloneHook = func(string) error { return nil }

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
	wantDiskPath := filepath.Join(clone.Path, "disk.img")
	if gotDiskPathOverride != wantDiskPath {
		t.Fatalf("runtimeSystemDiskPathOverride = %q, want %q", gotDiskPathOverride, wantDiskPath)
	}
	if gotAttachmentMode != systemDiskAttachmentTemporaryRAM {
		t.Fatalf("runtimeSystemDiskAttachment = %v, want %v", gotAttachmentMode, systemDiskAttachmentTemporaryRAM)
	}
	if runtimeSystemDiskPathOverride != oldRuntimeSystemDiskPathOverride {
		t.Fatalf("runtimeSystemDiskPathOverride after run = %q, want %q", runtimeSystemDiskPathOverride, oldRuntimeSystemDiskPathOverride)
	}
	if !strings.Contains(out, "System disk attachment: temporary-ram") {
		t.Fatalf("run output %q does not contain temporary RAM attachment line", out)
	}
}

func TestRunDisposableCloneFromDiskPathPreservesLinuxMode(t *testing.T) {
	stubAcquireRunLockHook(t)
	oldHome, homeErr := os.UserHomeDir()
	if homeErr != nil {
		t.Fatalf("UserHomeDir: %v", homeErr)
	}
	oldVMName := vmName
	oldVMDir := vmDir
	oldDisposableMode := disposableMode
	oldDisposableSourceDiskPath := disposableSourceDiskPath
	oldLinuxMode := linuxMode
	oldRuntimeSystemDiskPathOverride := runtimeSystemDiskPathOverride
	oldRuntimeSystemDiskAttachment := runtimeSystemDiskAttachment
	oldSetupHook := setupDisposableCloneHook
	oldCleanupHook := cleanupDisposableCloneHook
	oldRunMacHook := runMacOSVMHook
	oldRunLinuxHook := runLinuxVMHook
	t.Cleanup(func() {
		if err := os.Setenv("HOME", oldHome); err != nil {
			t.Fatalf("restore HOME: %v", err)
		}
		vmName = oldVMName
		vmDir = oldVMDir
		disposableMode = oldDisposableMode
		disposableSourceDiskPath = oldDisposableSourceDiskPath
		linuxMode = oldLinuxMode
		runtimeSystemDiskPathOverride = oldRuntimeSystemDiskPathOverride
		runtimeSystemDiskAttachment = oldRuntimeSystemDiskAttachment
		setupDisposableCloneHook = oldSetupHook
		cleanupDisposableCloneHook = oldCleanupHook
		runMacOSVMHook = oldRunMacHook
		runLinuxVMHook = oldRunLinuxHook
	})

	home := t.TempDir()
	t.Setenv("HOME", home)
	source := filepath.Join(vmconfig.BaseDir(), "linux-src")
	if err := os.MkdirAll(source, 0755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", source, err)
	}
	if err := os.WriteFile(filepath.Join(source, "linux-disk.img"), nil, 0644); err != nil {
		t.Fatalf("WriteFile(linux-disk.img): %v", err)
	}

	linuxMode = false
	vmName = "linux-src"
	vmDir = source

	clone := DisposableClone{
		Name: "linux-src-d-20260422-123456",
		Path: filepath.Join(vmconfig.BaseDir(), "linux-src-d-20260422-123456"),
	}
	var ranLinux bool

	setupDisposableCloneHook = func(opts DisposableSetupOptions) (DisposableClone, error) {
		if opts.Source != "linux-src" {
			t.Fatalf("SetupDisposableClone source = %q, want %q", opts.Source, "linux-src")
		}
		if opts.SourceDiskPath != "/tmp/checkpoint/linux-disk.img" {
			t.Fatalf("SetupDisposableClone SourceDiskPath = %q", opts.SourceDiskPath)
		}
		return clone, nil
	}
	runMacOSVMHook = func() error {
		t.Fatal("runMacOSVMHook should not be called for Linux disposable PIT runs")
		return nil
	}
	runLinuxVMHook = func() error {
		ranLinux = true
		if !linuxMode {
			t.Fatal("linuxMode = false during Linux disposable PIT run")
		}
		return nil
	}
	cleanupDisposableCloneHook = func(string) error { return nil }

	if _, err := captureStdoutResult(t, func() error {
		return runDisposableCloneFromDiskPath("linux-src", "/tmp/checkpoint/linux-disk.img", systemDiskAttachmentTemporaryRAM)
	}); err != nil {
		t.Fatalf("runDisposableCloneFromDiskPath() error = %v", err)
	}
	if !ranLinux {
		t.Fatal("runLinuxVMHook was not called")
	}
	if linuxMode {
		t.Fatal("linuxMode was not restored after disposable PIT run")
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
