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
	"github.com/tmc/cove/internal/iosbundle"
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
	return validateIOSFiles(hc.VMDir, saved.IOS)
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
	if rc.RawDisk {
		return fmt.Errorf("ios runtime requires a disk image")
	}
	if rc.GUI {
		return fmt.Errorf("ios runtime currently supports headless research runs only")
	}
	if rc.RecoveryMode {
		return fmt.Errorf("ios recovery requires the firmware restore pipeline; use force-dfu for research boot")
	}
	if rc.SaveCompress || rc.SaveEncrypt || rc.SkipResume || hasSuspendStateForVM(hc.VMDir) {
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
	if err := validateIOSFiles(hc.VMDir, saved.IOS); err != nil {
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
	defer func() {
		if err := stopIOSVM(func() error { return hardStopVMAndWait(vm, queue) }, func() (vz.VZVirtualMachineState, error) {
			vzkit.RunRunLoopOnce()
			return currentVMState(vm, queue)
		}, restartHardStopTimeout); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("stop ios vm: %w", err))
			noteVMRuntimeState(hc.VMDir, "error")
			return
		}
		noteVMRuntimeState(hc.VMDir, "stopped")
	}()
	startErr := beginVMStart(vm, queue, rc)
	if err := waitForVMStartPoll(startErr, func() (vz.VZVirtualMachineState, error) { return currentVMState(vm, queue) }, vmStartWaitOptions{Timeout: rc.StartTimeout, PumpRunLoop: true}); err != nil {
		return fmt.Errorf("start ios vm: %w", err)
	}
	noteVMRuntimeState(hc.VMDir, "running")
	fmt.Println("iOS research VM started; guest boot and DFU discovery are not verified. Send SIGINT or SIGTERM to stop.")
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-signals:
			return nil
		case <-ticker.C:
			vzkit.RunRunLoopOnce()
			state, err := currentVMState(vm, queue)
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

func stopIOSVM(stop func() error, poll func() (vz.VZVirtualMachineState, error), timeout time.Duration) error {
	if state, err := poll(); err == nil && state == vz.VZVirtualMachineStateStopped {
		return nil
	}
	stopErr := stop()
	state, waitErr := waitForVMStatePoll(poll, vz.VZVirtualMachineStateStopped, timeout, restartStatePollInterval)
	if state == vz.VZVirtualMachineStateStopped {
		return nil
	}
	return errors.Join(stopErr, waitErr)
}

func validateIOSFiles(dir string, cfg *iosbundle.Config) error {
	names := []string{"hw.model", "machine.id", "aux.img", "sep.img", "disk.img"}
	if cfg.ROM == "" {
		return fmt.Errorf("ios requires a prepared boot rom")
	}
	names = append(names, cfg.ROM)
	if cfg.SEPROM != "" {
		names = append(names, cfg.SEPROM)
	}
	for _, name := range names {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("ios state %s: %w", name, err)
		}
		if !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("ios state %s must be a nonempty regular file", name)
		}
	}
	return nil
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
