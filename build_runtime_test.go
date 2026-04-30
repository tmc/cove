package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

func TestWithBuildRuntimeGlobalsSetsAndRestores(t *testing.T) {
	oldVMDir := vmDir
	oldDiskPath := diskPath
	oldLinuxMode := linuxMode
	oldGUIMode := guiMode
	oldHeadlessMode := headlessMode
	oldSkipResume := skipResume
	oldRecoveryMode := recoveryMode
	oldBootArgs := bootArgs
	oldRunHTTPAddr := runHTTPAddr
	oldAutoMountVolumes := autoMountVolumes
	oldSerialOutput := serialOutput
	t.Cleanup(func() {
		vmDir = oldVMDir
		diskPath = oldDiskPath
		linuxMode = oldLinuxMode
		guiMode = oldGUIMode
		headlessMode = oldHeadlessMode
		skipResume = oldSkipResume
		recoveryMode = oldRecoveryMode
		bootArgs = oldBootArgs
		runHTTPAddr = oldRunHTTPAddr
		autoMountVolumes = oldAutoMountVolumes
		serialOutput = oldSerialOutput
	})

	vmDir = "old-vm"
	diskPath = "old-disk"
	linuxMode = false
	guiMode = true
	headlessMode = false
	skipResume = false
	recoveryMode = true
	bootArgs = "debug"
	runHTTPAddr = ":0"
	autoMountVolumes = true
	serialOutput = "stdout"

	sc := buildScratch{Dir: filepath.Join(t.TempDir(), "scratch"), DiskPath: filepath.Join(t.TempDir(), "scratch", "linux-disk.img")}
	err := withBuildRuntimeGlobals(sc, func() error {
		if vmDir != sc.Dir || diskPath != sc.DiskPath {
			return fmt.Errorf("paths = %q/%q, want %q/%q", vmDir, diskPath, sc.Dir, sc.DiskPath)
		}
		if !linuxMode || guiMode || !headlessMode || !skipResume || recoveryMode || bootArgs != "" || runHTTPAddr != "" || autoMountVolumes || serialOutput != "none" {
			return fmt.Errorf("unexpected runtime globals")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if vmDir != "old-vm" || diskPath != "old-disk" || linuxMode || !guiMode || headlessMode || skipResume || !recoveryMode || bootArgs != "debug" || runHTTPAddr != ":0" || !autoMountVolumes || serialOutput != "stdout" {
		t.Fatalf("runtime globals were not restored")
	}
}

func TestWithBuildRuntimeGlobalsRestoresAfterError(t *testing.T) {
	oldVMDir := vmDir
	t.Cleanup(func() { vmDir = oldVMDir })
	vmDir = "old-vm"
	wantErr := errors.New("failed")
	sc := buildScratch{Dir: filepath.Join(t.TempDir(), "scratch"), DiskPath: filepath.Join(t.TempDir(), "scratch", "disk.img")}
	err := withBuildRuntimeGlobals(sc, func() error { return wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("withBuildRuntimeGlobals() = %v, want %v", err, wantErr)
	}
	if vmDir != "old-vm" {
		t.Fatalf("vmDir = %q, want restored old-vm", vmDir)
	}
}

func TestWithBuildRuntimeGlobalsRejectsIncompleteScratch(t *testing.T) {
	if err := withBuildRuntimeGlobals(buildScratch{}, func() error { return nil }); err == nil {
		t.Fatal("withBuildRuntimeGlobals() error = nil, want missing dir")
	}
	if err := withBuildRuntimeGlobals(buildScratch{Dir: "scratch"}, func() error { return nil }); err == nil {
		t.Fatal("withBuildRuntimeGlobals() error = nil, want missing disk")
	}
}

func TestWaitBuildAgentRetriesUntilSuccess(t *testing.T) {
	restore := stubBuildControlSender(t, func(call *int, sock string, req *controlpb.ControlRequest, timeout time.Duration, cmdType string) (*controlpb.ControlResponse, error) {
		*call++
		if sock != "sock" || req.Type != "agent-ping" || cmdType != "agent-ping" {
			t.Fatalf("request = sock %q type %q cmd %q", sock, req.Type, cmdType)
		}
		if *call == 1 {
			return &controlpb.ControlResponse{Success: false, Error: "booting"}, nil
		}
		return &controlpb.ControlResponse{Success: true}, nil
	})
	defer restore()
	if err := waitBuildAgent(context.Background(), "sock", time.Second); err != nil {
		t.Fatalf("waitBuildAgent(): %v", err)
	}
}

func TestWaitBuildAgentHonorsContext(t *testing.T) {
	restore := stubBuildControlSender(t, func(call *int, sock string, req *controlpb.ControlRequest, timeout time.Duration, cmdType string) (*controlpb.ControlResponse, error) {
		*call++
		return nil, errors.New("unreachable")
	})
	defer restore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := waitBuildAgent(ctx, "sock", time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waitBuildAgent() = %v, want context.Canceled", err)
	}
}

func TestShutdownBuildGuest(t *testing.T) {
	restore := stubBuildControlSender(t, func(call *int, sock string, req *controlpb.ControlRequest, timeout time.Duration, cmdType string) (*controlpb.ControlResponse, error) {
		*call++
		if sock != "sock" || req.Type != "agent-shutdown" || req.GetAgentShutdown() == nil || cmdType != "agent-shutdown" {
			t.Fatalf("request = sock %q type %q cmd %q", sock, req.Type, cmdType)
		}
		return &controlpb.ControlResponse{Success: true}, nil
	})
	defer restore()
	if err := shutdownBuildGuest(context.Background(), "sock"); err != nil {
		t.Fatalf("shutdownBuildGuest(): %v", err)
	}
}

func TestShutdownBuildGuestReportsFailure(t *testing.T) {
	restore := stubBuildControlSender(t, func(call *int, sock string, req *controlpb.ControlRequest, timeout time.Duration, cmdType string) (*controlpb.ControlResponse, error) {
		*call++
		return &controlpb.ControlResponse{Success: false, Error: "denied"}, nil
	})
	defer restore()
	err := shutdownBuildGuest(context.Background(), "sock")
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("shutdownBuildGuest() = %v, want denial", err)
	}
}

func stubBuildControlSender(t *testing.T, fn func(*int, string, *controlpb.ControlRequest, time.Duration, string) (*controlpb.ControlResponse, error)) func() {
	t.Helper()
	old := sendBuildControlRequest
	calls := 0
	sendBuildControlRequest = func(sock string, req *controlpb.ControlRequest, timeout time.Duration, cmdType string) (*controlpb.ControlResponse, error) {
		return fn(&calls, sock, req, timeout, cmdType)
	}
	return func() {
		sendBuildControlRequest = old
	}
}
