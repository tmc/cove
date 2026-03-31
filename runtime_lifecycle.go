package main

import (
	"fmt"
	"os"
	"path/filepath"

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

	var clone DisposableClone
	if disposableMode {
		source := originalVMName
		if source == "" {
			source = filepathBase(originalVMDir)
		}
		created, err := setupDisposableCloneHook(DisposableSetupOptions{
			Source:        source,
			Linked:        true,
			CopyMachineID: false,
		})
		if err != nil {
			return err
		}
		clone = created
		vmName = clone.Name
		vmDir = clone.Path
		fmt.Printf("Disposable clone: %s\n", clone.Name)
		fmt.Printf("Disposable path: %s\n", clone.Path)
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

	if disposableMode {
		if cleanupErr := cleanupDisposableCloneHook(clone.Path); cleanupErr != nil {
			fmt.Fprintf(os.Stderr, "warning: cleanup disposable clone: %v\n", cleanupErr)
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
