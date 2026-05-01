package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tmc/apple/dispatch"
	vz "github.com/tmc/apple/virtualization"
)

var (
	setupDisposableCloneHook             = SetupDisposableClone
	cleanupDisposableCloneHook           = CleanupDisposableClone
	runMacOSVMHook                       = runMacOSVM
	runLinuxVMHook                       = runLinuxVM
	startPreparedFileHandleNetworkHook   = startPreparedFileHandleNetwork
	stopPreparedFileHandleNetworkHook    = stopPreparedFileHandleNetwork
	configureRequestedProxyAfterBootHook = configureRequestedProxyAfterBoot
	teardownRequestedProxyHook           = teardownRequestedProxy
	acquireRunLockHook                   = AcquireRunLock
)

type RunConfig struct {
	VM                       vmSelection
	Linux                    bool
	Disposable               bool
	RollbackSnapshot         string
	DisposableSourceDiskPath string
	SystemDiskAttachment     systemDiskAttachmentMode
	SystemDiskPathOverride   string
}

func currentRunConfig() RunConfig {
	return RunConfig{
		VM:                       currentVMSelection(),
		Linux:                    linuxMode,
		Disposable:               disposableMode,
		RollbackSnapshot:         rollbackSnapshotName,
		DisposableSourceDiskPath: disposableSourceDiskPath,
		SystemDiskAttachment:     runtimeSystemDiskAttachment,
		SystemDiskPathOverride:   runtimeSystemDiskPathOverride,
	}
}

func runCurrentVM() error {
	return runVMWithConfig(currentRunConfig())
}

func runVMWithConfig(cfg RunConfig) error {
	originalVMName := cfg.VM.Name
	originalVMDir := cfg.VM.Directory

	if cfg.Disposable && cfg.RollbackSnapshot != "" {
		return fmt.Errorf("rollback snapshot runs already create a disposable clone")
	}

	var clone DisposableClone
	temporaryClone := cfg.Disposable || cfg.RollbackSnapshot != ""
	if temporaryClone {
		source := originalVMName
		if source == "" {
			source = filepathBase(originalVMDir)
		}
		var (
			created DisposableClone
			err     error
		)
		if cfg.RollbackSnapshot != "" {
			created, err = setupRollbackSnapshotCloneHook(RollbackSnapshotCloneOptions{
				Source:   source,
				Snapshot: cfg.RollbackSnapshot,
			})
		} else {
			created, err = setupDisposableCloneHook(DisposableSetupOptions{
				Source:         source,
				Linked:         true,
				CopyMachineID:  false,
				SourceDiskPath: cfg.DisposableSourceDiskPath,
			})
		}
		if err != nil {
			return err
		}
		clone = created
		vmName = clone.Name
		vmDir = clone.Path
		if cfg.SystemDiskAttachment == systemDiskAttachmentTemporaryRAM && strings.TrimSpace(cfg.SystemDiskPathOverride) == "" {
			runtimeSystemDiskPathOverride = vmPrimaryDiskPath(clone.Path)
			defer func() {
				runtimeSystemDiskPathOverride = ""
			}()
		}
		if cfg.RollbackSnapshot != "" {
			fmt.Printf("Rollback snapshot: %s\n", cfg.RollbackSnapshot)
			fmt.Printf("Rollback clone: %s\n", clone.Name)
			fmt.Printf("Rollback path: %s\n", clone.Path)
		} else {
			fmt.Printf("Disposable clone: %s\n", clone.Name)
			fmt.Printf("Disposable path: %s\n", clone.Path)
		}
		if cfg.SystemDiskAttachment == systemDiskAttachmentTemporaryRAM {
			fmt.Printf("System disk attachment: %s\n", cfg.SystemDiskAttachment)
		}
		defer func() {
			vmName = originalVMName
			vmDir = originalVMDir
		}()
	}

	lock, err := acquireRunLockHook(vmDir)
	if err != nil {
		return fmt.Errorf("cove run: %w", err)
	}
	defer func() {
		if releaseErr := lock.Release(); releaseErr != nil {
			fmt.Fprintf(os.Stderr, "warning: release run.lock: %v\n", releaseErr)
		}
	}()

	var runErr error
	if cfg.Linux {
		runErr = runLinuxVMHook()
	} else {
		runErr = runMacOSVMHook()
	}

	if temporaryClone {
		if cleanupErr := cleanupDisposableCloneHook(clone.Path); cleanupErr != nil {
			fmt.Fprintf(os.Stderr, "warning: cleanup disposable clone: %v\n", cleanupErr)
		} else if cfg.RollbackSnapshot != "" {
			fmt.Printf("Rollback clone removed: %s\n", clone.Name)
		} else {
			fmt.Printf("Disposable clone removed: %s\n", clone.Name)
		}
	}

	return runErr
}

func startRuntimeFeatureServices(runtimeFeatures *runtimeFeatureState, vm vz.VZVirtualMachine, queue dispatch.Queue) {
	if runtimeFeatures == nil {
		return
	}
	if err := runtimeFeatures.startVMServices(vm, queue); err != nil {
		fmt.Printf("warning: runtime features: %v\n", err)
	}
}

func startControlRuntimeInfrastructure(controlServer *ControlServer) {
	startPreparedFileHandleNetworkHook()
	configureRequestedProxyAfterBootHook(controlServer)
}

func stopControlRuntimeInfrastructure(controlServer *ControlServer) {
	teardownRequestedProxyHook(controlServer)
	stopPreparedFileHandleNetworkHook()
	if controlServer != nil {
		controlServer.StopRuntimeFeatureState()
		controlServer.Stop()
	}
}

func filepathBase(path string) string {
	base := filepath.Base(path)
	switch base {
	case "", ".", "/":
		return ""
	default:
		return base
	}
}
