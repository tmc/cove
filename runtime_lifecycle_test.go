package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/lifecycle"
	"github.com/tmc/cove/internal/vmconfig"
	"github.com/tmc/cove/internal/vmpolicy"
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

func TestRunVMWithConfigEnforcesRunBudgetBeforeBoot(t *testing.T) {
	withTempHome(t)
	stubAcquireRunLockHook(t)

	runsRoot := t.TempDir()
	prevRuns := runsDirHook
	runsDirHook = func() string { return runsRoot }
	t.Cleanup(func() { runsDirHook = prevRuns })

	oldRunMacHook := runMacOSVMHook
	t.Cleanup(func() { runMacOSVMHook = oldRunMacHook })

	dir := t.TempDir()
	if err := vmpolicy.Save(dir, vmpolicy.Policy{RunBudget: 1}); err != nil {
		t.Fatalf("Save(): %v", err)
	}
	cfg := RunConfig{VM: vmSelection{Name: "budget-vm", Directory: dir}}

	runs := 0
	runMacOSVMHook = func() error {
		runs++
		return nil
	}
	if err := runVMWithConfig(cfg); err != nil {
		t.Fatalf("first runVMWithConfig: %v", err)
	}
	if runs != 1 {
		t.Fatalf("runs after first boot = %d, want 1", runs)
	}

	err := runVMWithConfig(cfg)
	if !errors.Is(err, lifecycle.ErrBudgetExceeded) {
		t.Fatalf("second runVMWithConfig error = %v, want ErrBudgetExceeded", err)
	}
	if runs != 1 {
		t.Fatalf("runs after budget exceeded = %d, want 1", runs)
	}
	used, err := lifecycle.RunsUsed(dir)
	if err != nil {
		t.Fatalf("RunsUsed(): %v", err)
	}
	if used != 1 {
		t.Fatalf("RunsUsed() = %d, want 1", used)
	}
	metricFiles, err := filepath.Glob(filepath.Join(runsRoot, "*", "metrics.jsonl"))
	if err != nil {
		t.Fatalf("Glob metrics: %v", err)
	}
	var found bool
	for _, path := range metricFiles {
		for _, event := range readMetricEvents(t, path) {
			if event.EventType != "lifecycle.budget.exceeded" {
				continue
			}
			found = true
			if event.Extra["vm_name"] != "budget-vm" {
				t.Fatalf("vm_name = %#v, want budget-vm", event.Extra["vm_name"])
			}
			if event.Extra["budget_count"] != float64(1) {
				t.Fatalf("budget_count = %#v, want 1", event.Extra["budget_count"])
			}
			if event.Extra["runs_used"] != float64(1) {
				t.Fatalf("runs_used = %#v, want 1", event.Extra["runs_used"])
			}
		}
	}
	if !found {
		t.Fatalf("lifecycle.budget.exceeded event not found in %v", metricFiles)
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

func TestRunEphemeralForkRejectsMacOSVMParentBeforeSetup(t *testing.T) {
	stubAcquireRunLockHook(t)
	t.Setenv("HOME", t.TempDir())
	parent := "identity-parent"
	stageParentVMForEphemeralFork(t, parent)

	oldVMName := vmName
	oldVMDir := vmDir
	oldRuntimeSystemDiskPathOverride := runtimeSystemDiskPathOverride
	oldRuntimeSystemDiskAttachment := runtimeSystemDiskAttachment
	oldSetupHook := setupEphemeralForkHook
	oldCleanupHook := cleanupEphemeralForkHook
	oldRunMacHook := runMacOSVMHook
	t.Cleanup(func() {
		vmName = oldVMName
		vmDir = oldVMDir
		runtimeSystemDiskPathOverride = oldRuntimeSystemDiskPathOverride
		runtimeSystemDiskAttachment = oldRuntimeSystemDiskAttachment
		setupEphemeralForkHook = oldSetupHook
		cleanupEphemeralForkHook = oldCleanupHook
		runMacOSVMHook = oldRunMacHook
	})

	setupEphemeralForkHook = func(opts EphemeralForkOptions) (EphemeralFork, error) {
		t.Fatalf("SetupEphemeralFork called for unsupported VM parent: %#v", opts)
		return EphemeralFork{}, nil
	}
	cleanupEphemeralForkHook = func(path string) error {
		t.Fatalf("CleanupEphemeralFork called before child exists: %s", path)
		return nil
	}
	runMacOSVMHook = func() error {
		t.Fatal("runMacOSVM called for unsupported VM-parent fork")
		return nil
	}

	cfg := RunConfig{
		VM:                  vmSelection{Name: "original", Directory: filepath.Join(vmconfig.BaseDir(), "original")},
		EphemeralForkParent: parent,
	}
	_, err := captureStdoutResult(t, func() error { return runVMWithConfig(cfg) })
	if err == nil {
		t.Fatal("runVMWithConfig succeeded for unsupported VM-parent fork")
	}
	msg := err.Error()
	for _, want := range []string{"RAM-overlay runtime", "not implemented", "No VM was created", "cove fork", "cove clone --linked", "image refs"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error = %q, want %q", msg, want)
		}
	}
	if runtimeSystemDiskPathOverride != oldRuntimeSystemDiskPathOverride {
		t.Fatalf("runtimeSystemDiskPathOverride after run = %q, want %q", runtimeSystemDiskPathOverride, oldRuntimeSystemDiskPathOverride)
	}
	if runtimeSystemDiskAttachment != oldRuntimeSystemDiskAttachment {
		t.Fatalf("runtimeSystemDiskAttachment after run = %v, want %v", runtimeSystemDiskAttachment, oldRuntimeSystemDiskAttachment)
	}
}

func TestRunEphemeralForkRejectsLinuxVMParentBeforeSetup(t *testing.T) {
	stubAcquireRunLockHook(t)
	t.Setenv("HOME", t.TempDir())
	parent := "linux-parent"
	parentDir := filepath.Join(vmconfig.BaseDir(), parent)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(parentDir, "linux-disk.img"), []byte("disk"), 0o644); err != nil {
		t.Fatalf("write linux disk: %v", err)
	}

	oldSetupHook := setupEphemeralForkHook
	oldCleanupHook := cleanupEphemeralForkHook
	oldRunLinuxHook := runLinuxVMHook
	t.Cleanup(func() {
		setupEphemeralForkHook = oldSetupHook
		cleanupEphemeralForkHook = oldCleanupHook
		runLinuxVMHook = oldRunLinuxHook
	})
	setupEphemeralForkHook = func(opts EphemeralForkOptions) (EphemeralFork, error) {
		t.Fatalf("SetupEphemeralFork called for unsupported Linux parent: %#v", opts)
		return EphemeralFork{}, nil
	}
	cleanupEphemeralForkHook = func(path string) error {
		t.Fatalf("CleanupEphemeralFork called before child exists: %s", path)
		return nil
	}
	runLinuxVMHook = func() error {
		t.Fatal("runLinuxVM called for unsupported VM-parent fork")
		return nil
	}

	cfg := RunConfig{
		Linux:               true,
		EphemeralForkParent: parent,
	}
	_, err := captureStdoutResult(t, func() error { return runVMWithConfig(cfg) })
	if err == nil {
		t.Fatal("runVMWithConfig succeeded for unsupported Linux VM-parent fork")
	}
	msg := err.Error()
	for _, want := range []string{"Linux", "VM-parent RAM-overlay forks are not implemented", "cove fork", "cove clone --linked"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error = %q, want %q", msg, want)
		}
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

func TestStartVMLifecyclePolicyMonitorNilNoop(t *testing.T) {
	// Nil ControlServer must short-circuit without spawning a goroutine
	// or panicking; the contract is "if cs == nil, return".
	startVMLifecyclePolicyMonitor(nil)
}
