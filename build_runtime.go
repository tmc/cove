package main

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

type buildControlSender func(string, *controlpb.ControlRequest, time.Duration, string) (*controlpb.ControlResponse, error)
type buildGuestCleanup func(context.Context) error
type buildGuestStarter func(context.Context, buildScratch) (buildGuestCleanup, error)

var sendBuildControlRequest buildControlSender = ctlSendRequest

func (e *buildExecutor) startBuildGuest(ctx context.Context, sc buildScratch) (buildGuestCleanup, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e.startGuest != nil {
		return e.startGuest(ctx, sc)
	}
	return func(context.Context) error { return nil }, nil
}

func withBuildRuntimeGlobals(sc buildScratch, fn func() error) error {
	if sc.Dir == "" {
		return fmt.Errorf("build runtime: scratch vm dir required")
	}
	if sc.DiskPath == "" {
		return fmt.Errorf("build runtime: scratch disk path required")
	}
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
	defer func() {
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
	}()

	vmDir = sc.Dir
	diskPath = sc.DiskPath
	linuxMode = filepath.Base(sc.DiskPath) == "linux-disk.img"
	guiMode = false
	headlessMode = true
	skipResume = true
	recoveryMode = false
	bootArgs = ""
	runHTTPAddr = ""
	autoMountVolumes = false
	serialOutput = "none"
	return fn()
}

func waitBuildAgent(ctx context.Context, socketPath string, timeout time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if socketPath == "" {
		return fmt.Errorf("build agent wait: control socket path required")
	}
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	var last error
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		req := &controlpb.ControlRequest{Type: "agent-ping"}
		resp, err := sendBuildControlRequest(socketPath, req, 5*time.Second, "agent-ping")
		if err == nil && resp.Success {
			return nil
		}
		if err != nil {
			last = err
		} else {
			last = fmt.Errorf("agent-ping: %s", resp.Error)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("build agent wait: %w", last)
		}
		timer := time.NewTimer(500 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func shutdownBuildGuest(ctx context.Context, socketPath string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if socketPath == "" {
		return fmt.Errorf("build shutdown: control socket path required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	req := &controlpb.ControlRequest{
		Type: "agent-shutdown",
		Command: &controlpb.ControlRequest_AgentShutdown{
			AgentShutdown: &controlpb.AgentShutdownCommand{},
		},
	}
	resp, err := sendBuildControlRequest(socketPath, req, 30*time.Second, "agent-shutdown")
	if err != nil {
		return err
	}
	if !resp.Success {
		return fmt.Errorf("build shutdown: %s", resp.Error)
	}
	return nil
}
