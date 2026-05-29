package main

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/tmc/apple/dispatch"
	vz "github.com/tmc/apple/virtualization"
	agentstate "github.com/tmc/cove/internal/agent"
	"github.com/tmc/cove/internal/buildscratch"
	"github.com/tmc/cove/internal/vmrun"
	controlpb "github.com/tmc/cove/proto/controlpb"
)

type buildControlSender func(string, *controlpb.ControlRequest, time.Duration, string) (*controlpb.ControlResponse, error)
type buildGuestCleanup func(context.Context) error
type buildGuestStarter func(context.Context, buildscratch.Scratch) (buildGuestCleanup, error)

var (
	sendBuildControlRequest buildControlSender = ctlSendRequest
	defaultBuildGuestStart  buildGuestStarter  = startScratchBuildGuest
	defaultBuildCompact     buildCompactor     = compactBuildScratch
	defaultBuildSecretMount buildSecretMounter = mountBuildStepSecrets
)

func (e *buildExecutor) startBuildGuest(ctx context.Context, sc buildscratch.Scratch) (buildGuestCleanup, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e.startGuest != nil {
		return e.startGuest(ctx, sc)
	}
	return defaultBuildGuestStart(ctx, sc)
}

func withBuildRuntimeGlobals(sc buildscratch.Scratch, fn func() error) error {
	if sc.Dir == "" {
		return fmt.Errorf("build runtime: scratch vm dir required")
	}
	if sc.DiskPath == "" {
		return fmt.Errorf("build runtime: scratch disk path required")
	}
	oldVMDir := vmDir
	oldDiskPath := diskPath
	oldLinuxMode := linuxMode
	oldWindowsMode := windowsMode
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
		windowsMode = oldWindowsMode
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
	diskBase := filepath.Base(sc.DiskPath)
	linuxMode = diskBase == "linux-disk.img"
	windowsMode = diskBase == "windows-disk.img"
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

func startScratchBuildGuest(ctx context.Context, sc buildscratch.Scratch) (buildGuestCleanup, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var runtime *scratchBuildGuestRuntime
	err := withBuildRuntimeGlobals(sc, func() error {
		config, err := buildSelectedVMFrameworkConfiguration(sc.DiskPath)
		if err != nil {
			return fmt.Errorf("build configuration: %w", err)
		}
		config.Retain()
		updateSaveRestoreSupport(config)

		queue := dispatch.QueueCreate("com.appledocs.vz.build.vmqueue")
		vm := vz.NewVirtualMachineWithConfigurationQueue(&config, queue)
		if vm.ID == 0 {
			return fmt.Errorf("failed to create virtual machine")
		}
		vm.Retain()

		sock := GetControlSocketPathForVM(sc.Dir)
		controlServer := NewControlServerWithVMDir(sock, sc.Dir)
		controlServer.SetVM(vm, queue)
		guest := vmrun.GuestMacOS
		if linuxMode {
			guest = vmrun.GuestLinux
		} else if windowsMode {
			guest = vmrun.GuestWindows
		}
		rc, hc := vmrunRunConfig(guest), vmrunHostConfig()
		controlServer.SetRunContext(rc, hc)
		if err := controlServer.Start(); err != nil {
			return fmt.Errorf("control socket: %w", err)
		}
		startControlRuntimeInfrastructure(controlServer)
		runtime = &scratchBuildGuestRuntime{
			vm:            vm,
			queue:         queue,
			controlServer: controlServer,
		}
		if err := startConfiguredVM(vm, queue, true, nil, rc, hc); err != nil {
			return fmt.Errorf("start vm: %w", err)
		}
		return nil
	})
	if err != nil {
		if runtime != nil {
			_ = runtime.cleanup(ctx)
		}
		return nil, err
	}
	return runtime.cleanup, nil
}

type scratchBuildGuestRuntime struct {
	vm            vz.VZVirtualMachine
	queue         dispatch.Queue
	controlServer *ControlServer
	stopOnce      sync.Once
	stopErr       error
}

func (r *scratchBuildGuestRuntime) cleanup(ctx context.Context) error {
	if r == nil {
		return nil
	}
	r.stopOnce.Do(func() {
		if ctx == nil {
			ctx = context.Background()
		}
		stopControlRuntimeInfrastructure(r.controlServer)
		if err := stopScratchBuildVM(ctx, r.vm, r.queue); err != nil {
			r.stopErr = err
		}
	})
	return r.stopErr
}

func stopScratchBuildVM(ctx context.Context, vm vz.VZVirtualMachine, queue dispatch.Queue) error {
	if ctx == nil {
		ctx = context.Background()
	}
	state, err := currentVMState(vm, queue)
	if err != nil {
		return fmt.Errorf("build vm state: %w", err)
	}
	switch state {
	case vz.VZVirtualMachineStateStopped, vz.VZVirtualMachineStateError:
		return nil
	}
	hardStopVM(vm, queue)
	return waitScratchBuildVMStopped(ctx, vm, queue, 30*time.Second)
}

func waitScratchBuildVMStopped(ctx context.Context, vm vz.VZVirtualMachine, queue dispatch.Queue, timeout time.Duration) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		state, err := currentVMState(vm, queue)
		if err != nil {
			return fmt.Errorf("build vm state: %w", err)
		}
		switch state {
		case vz.VZVirtualMachineStateStopped, vz.VZVirtualMachineStateError:
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("build vm stop: %w", ctx.Err())
		case <-tick.C:
		}
	}
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

func compactBuildScratch(ctx context.Context, sc buildscratch.Scratch, mode string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateCompactMode(mode); err != nil {
		return err
	}
	if sc.Dir == "" {
		return fmt.Errorf("build compact: scratch vm dir required")
	}
	switch mode {
	case "fast":
		return nil
	case "targeted":
		return compactBuildScratchTargeted(ctx, sc)
	case "thorough":
		if _, err := compactVM(sc.Dir); err != nil {
			return err
		}
		return ctx.Err()
	default:
		return fmt.Errorf("compact: invalid mode %q", mode)
	}
}

func compactBuildScratchTargeted(ctx context.Context, sc buildscratch.Scratch) error {
	script, err := targetedBuildCompactScript(agentstate.Platform(sc.Dir))
	if err != nil {
		return err
	}
	return runBuildAgentShell(ctx, GetControlSocketPathForVM(sc.Dir), script)
}

func targetedBuildCompactScript(platform string) (string, error) {
	switch platform {
	case agentstate.PlatformLinux:
		return "set +e; rm -rf /var/log/* /var/cache/* /tmp/*; sync", nil
	case agentstate.PlatformMacOS:
		return "set +e; rm -rf /private/var/log/* /var/log/* /var/db/diagnostics/* /private/var/folders/*/C/*; sync", nil
	default:
		return "", fmt.Errorf("unsupported guest platform %q", platform)
	}
}
