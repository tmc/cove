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
)

func runCurrentVM() error {
	originalVMName := vmName
	originalVMDir := vmDir

	if disposableMode && rollbackSnapshotName != "" {
		return fmt.Errorf("rollback snapshot runs already create a disposable clone")
	}

	var clone DisposableClone
	temporaryClone := disposableMode || rollbackSnapshotName != ""
	if temporaryClone {
		source := originalVMName
		if source == "" {
			source = filepathBase(originalVMDir)
		}
		var (
			created DisposableClone
			err     error
		)
		if rollbackSnapshotName != "" {
			created, err = setupRollbackSnapshotCloneHook(RollbackSnapshotCloneOptions{
				Source:   source,
				Snapshot: rollbackSnapshotName,
			})
		} else {
			created, err = setupDisposableCloneHook(DisposableSetupOptions{
				Source:         source,
				Linked:         true,
				CopyMachineID:  false,
				SourceDiskPath: disposableSourceDiskPath,
			})
		}
		if err != nil {
			return err
		}
		clone = created
		vmName = clone.Name
		vmDir = clone.Path
		if runtimeSystemDiskAttachment == systemDiskAttachmentTemporaryRAM && strings.TrimSpace(runtimeSystemDiskPathOverride) == "" {
			runtimeSystemDiskPathOverride = vmPrimaryDiskPath(clone.Path)
			defer func() {
				runtimeSystemDiskPathOverride = ""
			}()
		}
		if rollbackSnapshotName != "" {
			fmt.Printf("Rollback snapshot: %s\n", rollbackSnapshotName)
			fmt.Printf("Rollback clone: %s\n", clone.Name)
			fmt.Printf("Rollback path: %s\n", clone.Path)
		} else {
			fmt.Printf("Disposable clone: %s\n", clone.Name)
			fmt.Printf("Disposable path: %s\n", clone.Path)
		}
		if runtimeSystemDiskAttachment == systemDiskAttachmentTemporaryRAM {
			fmt.Printf("System disk attachment: %s\n", runtimeSystemDiskAttachment)
		}
		defer func() {
			vmName = originalVMName
			vmDir = originalVMDir
		}()
	}

	var runErr error
	if linuxMode {
		runErr = runLinuxVMHook()
	} else {
		runErr = runMacOSVMHook()
	}

	if temporaryClone {
		if cleanupErr := cleanupDisposableCloneHook(clone.Path); cleanupErr != nil {
			fmt.Fprintf(os.Stderr, "warning: cleanup disposable clone: %v\n", cleanupErr)
		} else if rollbackSnapshotName != "" {
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
