package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tmc/apple/dispatch"
	vz "github.com/tmc/apple/virtualization"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

var (
	setupDisposableCloneHook             = SetupDisposableClone
	cleanupDisposableCloneHook           = CleanupDisposableClone
	setupEphemeralForkHook               = SetupEphemeralFork
	cleanupEphemeralForkHook             = CleanupEphemeralFork
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
	// EphemeralForkParent triggers Phase 3 RAM-overlay ephemeral mode:
	// boot a short-lived sibling that shares the parent's disk.img
	// read-only and discards writes on shutdown. Mutually exclusive
	// with Disposable and RollbackSnapshot.
	EphemeralForkParent string
	EphemeralForkName   string
	EphemeralForkKeep   bool
	// Ephemeral marks an image-fork-from child for destroy-on-stop
	// using the .ephemeral sentinel. Slice 1 of design 024.
	Ephemeral bool
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
		EphemeralForkParent:      ephemeralForkParent,
		EphemeralForkName:        ephemeralForkName,
		EphemeralForkKeep:        ephemeralForkKeep,
		Ephemeral:                runEphemeral,
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
	if cfg.EphemeralForkParent != "" && (cfg.Disposable || cfg.RollbackSnapshot != "") {
		return fmt.Errorf("-fork-from is not compatible with -disposable or rollback snapshot runs")
	}

	if cfg.EphemeralForkParent != "" {
		// If <ref> resolves to a local image (and not a VM name), take
		// the image-fork-from path: clonefile-materialize a fresh bundle
		// and boot it. Falls through to the legacy RAM-overlay path when
		// the ref is a VM name.
		if isImageForkFromRef(cfg.EphemeralForkParent) {
			return runImageForkFromWithConfig(cfg, originalVMName, originalVMDir)
		}
		return runEphemeralForkWithConfig(cfg, originalVMName, originalVMDir)
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

// runEphemeralForkWithConfig boots a Phase 3 ephemeral sibling: an
// in-memory child that shares the parent's disk.img read-only via
// VZTemporaryRAMStorageDeviceAttachment. The child's vmDir is
// auto-removed on exit unless cfg.EphemeralForkKeep is set.
func runEphemeralForkWithConfig(cfg RunConfig, originalVMName, originalVMDir string) error {
	parentDir := vmconfig.Path(cfg.EphemeralForkParent)
	if !vmconfig.Validate(parentDir) {
		return fmt.Errorf("cove run -fork-from: parent VM not found: %s", cfg.EphemeralForkParent)
	}

	// Probe-and-release the parent's run.lock. If we can't acquire
	// LOCK_EX, the parent is running and we refuse to attach to its
	// disk.img. Validation #1 showed VZ takes no file lock at attach
	// time, so this guard is enforced on our side.
	parentLock, err := acquireRunLockHook(parentDir)
	if err != nil {
		if errors.Is(err, ErrRunLockHeld) {
			return fmt.Errorf("cove run -fork-from: parent VM %q is running; ephemeral fork requires parent stopped", cfg.EphemeralForkParent)
		}
		return fmt.Errorf("cove run -fork-from: probe parent run.lock: %w", err)
	}
	if releaseErr := parentLock.Release(); releaseErr != nil {
		fmt.Fprintf(os.Stderr, "warning: release parent run.lock: %v\n", releaseErr)
	}

	fork, err := setupEphemeralForkHook(EphemeralForkOptions{
		Parent: cfg.EphemeralForkParent,
		Name:   cfg.EphemeralForkName,
	})
	if err != nil {
		return err
	}

	fmt.Printf("Ephemeral fork: %s\n", fork.Name)
	fmt.Printf("Ephemeral path: %s\n", fork.Path)
	fmt.Printf("Parent disk:    %s (RAM-overlay, read-only)\n", vmPrimaryDiskPath(parentDir))

	parentDisk := vmPrimaryDiskPath(parentDir)
	prevAttachment := runtimeSystemDiskAttachment
	prevOverride := runtimeSystemDiskPathOverride
	runtimeSystemDiskAttachment = systemDiskAttachmentTemporaryRAM
	runtimeSystemDiskPathOverride = parentDisk
	defer func() {
		runtimeSystemDiskAttachment = prevAttachment
		runtimeSystemDiskPathOverride = prevOverride
	}()

	vmName = fork.Name
	vmDir = fork.Path
	defer func() {
		vmName = originalVMName
		vmDir = originalVMDir
	}()

	lock, err := acquireRunLockHook(vmDir)
	if err != nil {
		// Lock acquisition failed before booting; remove the dir so
		// no orphan is left behind (it won't have been used).
		_ = cleanupEphemeralForkHook(fork.Path)
		return fmt.Errorf("cove run -fork-from: %w", err)
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

	if cfg.EphemeralForkKeep {
		fmt.Printf("Ephemeral fork retained (-keep): %s\n", fork.Path)
		return runErr
	}
	if cleanupErr := cleanupEphemeralForkHook(fork.Path); cleanupErr != nil {
		fmt.Fprintf(os.Stderr, "warning: cleanup ephemeral fork: %v\n", cleanupErr)
	} else {
		fmt.Printf("Ephemeral fork removed: %s\n", fork.Name)
	}
	return runErr
}
