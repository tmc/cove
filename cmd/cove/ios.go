package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/tmc/apple/dispatch"
	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/apple/x/vzkit"
	"github.com/tmc/cove/internal/vmconfig"
	"github.com/tmc/cove/internal/vmpolicy"
	"github.com/tmc/cove/internal/vmrun"
)

func resolveIOSRun(cfg RunConfig, rc *vmrun.RunConfig, hc vmrun.HostConfig) error {
	dir := hc.VMDir
	if cfg.EphemeralForkParent != "" && !isImageForkFromRef(cfg.EphemeralForkParent) {
		dir = vmconfig.Path(cfg.EphemeralForkParent)
	}
	saved, err := vmconfig.Load(dir)
	if err != nil {
		return err
	}
	if saved.IOS == nil {
		if rc.OS == vmrun.GuestIOS {
			return fmt.Errorf("ios runtime requires an ios configuration")
		}
		return nil
	}
	if cfg.Linux || cfg.Windows || rc.OS == vmrun.GuestLinux || rc.OS == vmrun.GuestWindows {
		return fmt.Errorf("ios bundle conflicts with the selected guest operating system")
	}
	rc.OS = vmrun.GuestIOS
	if cfg.SystemDiskPathOverride != "" || cfg.SystemDiskAttachment != systemDiskAttachmentDiskImage {
		return fmt.Errorf("ios runtime requires the bundle disk.img consistency set")
	}
	if cfg.Disposable || cfg.RollbackSnapshot != "" || cfg.EphemeralForkParent != "" || cfg.Ephemeral {
		return fmt.Errorf("ios clone, fork and disposable lifecycle are not supported")
	}
	if err := validateIOSRun(*rc, hc); err != nil {
		return err
	}
	if len(saved.Volumes) != 0 {
		return fmt.Errorf("ios does not support saved shared folders")
	}
	return saved.IOS.ValidatePrepared(hc.VMDir)
}

func validateIOSRun(rc vmrun.RunConfig, hc vmrun.HostConfig) error {
	if err := rc.Validate(); err != nil {
		return err
	}
	if rc.OS != vmrun.GuestIOS {
		return fmt.Errorf("ios runtime requires the ios guest type")
	}
	policy, err := vmpolicy.Load(hc.VMDir)
	if err != nil {
		return err
	}
	if policy.IdleTimeout > 0 || policy.MaxAge > 0 {
		return fmt.Errorf("ios runtime does not support idle or maximum-age policies")
	}
	if rc.SerialOutput != "" && rc.SerialOutput != "none" && rc.SerialOutput != "stdout" {
		return fmt.Errorf("ios serial output supports stdout or none")
	}
	if rc.RawDisk {
		return fmt.Errorf("ios runtime requires a disk image")
	}
	if rc.GUI {
		return fmt.Errorf("ios runtime currently supports headless research runs only")
	}
	if rc.RecoveryMode {
		return fmt.Errorf("ios recovery requires the firmware restore pipeline; use force-dfu for research boot")
	}
	_, suspendErr := os.Stat(filepath.Join(hc.VMDir, "suspend.vmstate"))
	if rc.SaveCompress || rc.SaveEncrypt || rc.SkipResume || suspendErr == nil {
		return fmt.Errorf("ios save and resume are not supported; preserve saved state outside the active bundle")
	}
	if hc.RecoverIdentity {
		return fmt.Errorf("ios identity recovery is not supported")
	}
	if rc.Unattended || rc.ProvisionUser != "" || rc.ProvisionPassword != "" || rc.BootCommandsFile != "" || len(rc.StartupForwards) != 0 || rc.HTTPListenAddr != "" {
		return fmt.Errorf("ios guest provisioning, automation and forwarding are not supported")
	}
	if len(rc.USB) != 0 || len(rc.BlockDevices) != 0 || len(rc.Displays) != 0 || rc.GDBAddress != "" || rc.GDBListenAll {
		return fmt.Errorf("ios runtime does not support extra devices, display overrides or debug stubs")
	}
	if rc.BootArgs != "" {
		return fmt.Errorf("ios runtime boot arguments must be prepared in persistent nvram")
	}
	if rc.DiskPath != "" && filepath.Clean(rc.DiskPath) != filepath.Join(hc.VMDir, "disk.img") {
		return fmt.Errorf("ios runtime requires the bundle disk.img consistency set")
	}
	return nil
}

func runIOSVMWithConfig(rc vmrun.RunConfig, hc vmrun.HostConfig, bundle *RunBundle, metrics runMetricRecorder) (runErr error) {
	if err := validateIOSRun(rc, hc); err != nil {
		return err
	}
	saved, err := vmconfig.Load(hc.VMDir)
	if err != nil {
		return err
	}
	if saved.IOS == nil {
		return fmt.Errorf("ios runtime requires an ios configuration")
	}
	if len(saved.Volumes) != 0 {
		return fmt.Errorf("ios does not support saved shared folders")
	}
	if err := saved.IOS.ValidatePrepared(hc.VMDir); err != nil {
		return err
	}
	config, err := buildIOSVMConfiguration(rc, hc, saved.IOS)
	if err != nil {
		return fmt.Errorf("build ios configuration: %w", err)
	}
	config.Retain()
	defer config.Release()
	valid, err := config.ValidateWithError()
	if err != nil {
		return fmt.Errorf("validate ios configuration: %w", err)
	}
	if !valid {
		return fmt.Errorf("ios configuration validation failed")
	}
	queue := dispatch.QueueCreate("com.github.tmc.cove.ios.vmqueue")
	vm := vz.NewVirtualMachineWithConfigurationQueue(&config, queue)
	if vm.ID == 0 {
		return fmt.Errorf("create ios virtual machine")
	}
	vm.Retain()
	defer vm.Release()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	return runIOSLifecycle(
		func() <-chan error { return beginVMStart(vm, queue, rc) },
		func() <-chan error {
			result := make(chan error, 1)
			DispatchAsyncQueue(queue, func() {
				vm.StopWithCompletionHandler(func(err error) { result <- snapshotNSError(err) })
			})
			return result
		},
		func() (vz.VZVirtualMachineState, error) { return currentVMState(vm, queue) },
		signals, rc.StartTimeout, restartHardStopTimeout, vzkit.RunRunLoopOnce,
		func(state string) {
			noteVMRuntimeState(hc.VMDir, state)
			if state == "running" {
				fmt.Println("iOS research VM started; guest boot and DFU discovery are not verified. Send SIGINT or SIGTERM to stop.")
			}
		},
	)
}

var errIOSInterrupted = errors.New("ios startup interrupted")

func runIOSLifecycle(start, stop func() <-chan error, poll func() (vz.VZVirtualMachineState, error), signals <-chan os.Signal, startTimeout, stopTimeout time.Duration, pump func(), report func(string)) (runErr error) {
	defer func() {
		if err := stopIOSVM(stop, poll, stopTimeout, pump); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("stop ios vm: %w", err))
			report("error")
			return
		}
		report("stopped")
	}()
	if err := waitForIOSStart(start(), poll, signals, startTimeout, pump); err != nil {
		if errors.Is(err, errIOSInterrupted) {
			return nil
		}
		return fmt.Errorf("start ios vm: %w", err)
	}
	report("running")
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-signals:
			return nil
		case <-ticker.C:
			pump()
			state, err := poll()
			if err != nil {
				return fmt.Errorf("read ios vm state: %w", err)
			}
			switch state {
			case vz.VZVirtualMachineStateStopped:
				return nil
			case vz.VZVirtualMachineStateError:
				return fmt.Errorf("ios vm entered error state")
			}
		}
	}
}

func waitForIOSStart(result <-chan error, poll func() (vz.VZVirtualMachineState, error), signals <-chan os.Signal, timeout time.Duration, pump func()) error {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	completed := false
	for {
		select {
		case <-signals:
			return errIOSInterrupted
		case err := <-result:
			if err != nil && !errors.Is(err, errVMStartupInProgress) {
				return err
			}
			completed = err == nil
			result = nil
		case <-deadline.C:
			return fmt.Errorf("ios vm start timed out")
		case <-ticker.C:
			pump()
			state, err := poll()
			if err != nil {
				continue
			}
			switch state {
			case vz.VZVirtualMachineStateRunning:
				return nil
			case vz.VZVirtualMachineStateError:
				return fmt.Errorf("ios vm entered error state during startup")
			case vz.VZVirtualMachineStateStopped:
				if completed {
					return fmt.Errorf("ios vm stopped during startup")
				}
			}
		}
	}
}

func stopIOSVM(stop func() <-chan error, poll func() (vz.VZVirtualMachineState, error), timeout time.Duration, pump func()) error {
	if state, err := poll(); err == nil && state == vz.VZVirtualMachineStateStopped {
		return nil
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	result := stop()
	var stopErr error
	for {
		pump()
		if state, err := poll(); err == nil && state == vz.VZVirtualMachineStateStopped {
			return nil
		}
		select {
		case err := <-result:
			stopErr = err
			result = nil
		case <-deadline.C:
			return errors.Join(stopErr, fmt.Errorf("ios vm stop timed out before reaching stopped state"))
		case <-ticker.C:
		}
	}
}

func applyIOSRuntimeDefaults(cfg *RunConfig) {
	if vmconfig.DetectOSType(cfg.VM.Directory) != "iOS" {
		return
	}
	if !flagWasProvided(flag.CommandLine, "rosetta") {
		cfg.VMRun.EnableRosetta = false
	}
	if !flagWasProvided(flag.CommandLine, "clipboard") {
		cfg.VMRun.EnableClipboard = false
	}
}
