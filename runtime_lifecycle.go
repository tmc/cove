package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tmc/apple/dispatch"
	vz "github.com/tmc/apple/virtualization"

	"github.com/tmc/vz-macos/internal/vmconfig"
	"github.com/tmc/vz-macos/internal/vmpolicy"
)

var (
	setupDisposableCloneHook             = SetupDisposableClone
	cleanupDisposableCloneHook           = CleanupDisposableClone
	setupEphemeralForkHook               = SetupEphemeralFork
	cleanupEphemeralForkHook             = CleanupEphemeralFork
	runMacOSVMHook                       = runMacOSVM
	runLinuxVMHook                       = runLinuxVM
	runWindowsVMHook                     = runWindowsVM
	startPreparedFileHandleNetworkHook   = startPreparedFileHandleNetwork
	stopPreparedFileHandleNetworkHook    = stopPreparedFileHandleNetwork
	configureRequestedProxyAfterBootHook = configureRequestedProxyAfterBoot
	teardownRequestedProxyHook           = teardownRequestedProxy
	acquireRunLockHook                   = AcquireRunLock
)

type RunConfig struct {
	VM                       vmSelection
	Linux                    bool
	Windows                  bool
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
		Windows:                  windowsMode,
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
		// Per-run artifact bundling is enabled only for the ephemeral
		// fork-from paths — these are short-lived jobs the operator
		// later wants to bisect. Plain `cove run <vm>` is a long-lived
		// workstation and is intentionally excluded.
		bundle, err := beginRunBundle(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: run bundle init: %v\n", err)
		}
		writeActiveNetworkPolicyAudit(bundle)

		// If <ref> resolves to a local image (and not a VM name), take
		// the image-fork-from path: clonefile-materialize a fresh bundle
		// and boot it. Falls through to the legacy RAM-overlay path when
		// the ref is a VM name.
		var runErr error
		if isImageForkFromRef(cfg.EphemeralForkParent) {
			runErr = runImageForkFromWithConfig(cfg, originalVMName, originalVMDir)
		} else {
			runErr = runEphemeralForkWithConfig(cfg, originalVMName, originalVMDir)
		}
		finishRunBundle(bundle, runErr)
		return runErr
	}

	metricsRun, err := beginStandaloneMetricsRun(originalVMName, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: metrics init: %v\n", err)
	}
	writeActiveNetworkPolicyAudit(metricsRun)
	defer finishStandaloneMetricsRun(metricsRun)

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
	if cfg.Windows {
		runErr = runWindowsVMHook()
	} else if cfg.Linux {
		runErr = runLinuxVMHook()
	} else {
		runErr = runMacOSVMHook()
	}
	exit := "ok"
	if runErr != nil {
		exit = runErr.Error()
	}
	if metricsRun != nil {
		emitMetricEvent("run_complete", metricsRun.started, exit, nil)
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
	startVMLifecyclePolicyMonitor(controlServer)
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

type lifecycleTicker interface {
	C() <-chan time.Time
	Stop()
}

type lifecycleClock interface {
	Now() time.Time
	NewTicker(time.Duration) lifecycleTicker
}

type systemLifecycleClock struct{}

func (systemLifecycleClock) Now() time.Time { return time.Now() }

func (systemLifecycleClock) NewTicker(d time.Duration) lifecycleTicker {
	return &systemLifecycleTicker{Ticker: time.NewTicker(d)}
}

type systemLifecycleTicker struct {
	*time.Ticker
}

func (t *systemLifecycleTicker) C() <-chan time.Time { return t.Ticker.C }

var vmLifecycleClock lifecycleClock = systemLifecycleClock{}
var lifecycleCurrentVMStateHook = func(s *ControlServer) (vz.VZVirtualMachineState, error) {
	return s.currentVMState()
}
var lifecycleRequestStopHook = func(s *ControlServer) error {
	resp := s.requestStopVM()
	if resp != nil && resp.Error != "" {
		return errors.New(resp.Error)
	}
	return nil
}

const vmPolicyMonitorInterval = 5 * time.Second

func startVMLifecyclePolicyMonitor(controlServer *ControlServer) {
	if controlServer == nil {
		return
	}
	go controlServer.runVMLifecyclePolicyMonitor()
}

func (s *ControlServer) runVMLifecyclePolicyMonitor() {
	ctx := s.lifecycleContext()
	ticker := vmLifecycleClock.NewTicker(vmPolicyMonitorInterval)
	defer ticker.Stop()

	s.checkVMLifecyclePolicy()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			s.checkVMLifecyclePolicy()
		}
	}
}

func (s *ControlServer) checkVMLifecyclePolicy() {
	policy, err := vmpolicy.Load(s.effectiveVMDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: vm policy load: %v\n", err)
		return
	}
	if policy.Empty() {
		return
	}

	state, err := lifecycleCurrentVMStateHook(s)
	if err != nil {
		return
	}
	if state != vz.VZVirtualMachineStateRunning && state != vz.VZVirtualMachineStatePaused {
		return
	}

	startedAt, execCount, stopIssued := s.policySnapshot()
	if stopIssued {
		return
	}

	now := vmLifecycleClock.Now()
	reason := ""
	switch {
	case policy.RunBudget > 0 && execCount >= int64(policy.RunBudget):
		reason = "run_budget"
	case policy.MaxAge > 0 && !startedAt.IsZero() && now.Sub(startedAt) >= policy.MaxAge:
		reason = "max_age"
	case policy.IdleTimeout > 0 && state == vz.VZVirtualMachineStateRunning:
		s.healthMu.RLock()
		lastPing := s.agentHealth.lastPing
		s.healthMu.RUnlock()
		if !lastPing.IsZero() && now.Sub(lastPing) >= policy.IdleTimeout {
			reason = "idle"
		}
	}
	if reason == "" {
		return
	}
	if !s.markPolicyStopIssued() {
		return
	}

	emitMetricEvent("vm_policy_stop", startedAt, "ok", map[string]any{
		"reason":       reason,
		"policy_path":  vmpolicy.Path(s.effectiveVMDir()),
		"idle_timeout": policy.IdleTimeout.String(),
		"max_age":      policy.MaxAge.String(),
		"run_budget":   policy.RunBudget,
		"exec_count":   execCount,
	})
	if err := lifecycleRequestStopHook(s); err != nil {
		fmt.Fprintf(os.Stderr, "warning: vm policy stop: %v\n", err)
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

	forkStarted := time.Now()
	fork, err := setupEphemeralForkHook(EphemeralForkOptions{
		Parent:           cfg.EphemeralForkParent,
		Name:             cfg.EphemeralForkName,
		PreserveIdentity: true,
	})
	if err != nil {
		return err
	}

	fmt.Printf("Ephemeral fork: %s\n", fork.Name)
	fmt.Printf("Ephemeral path: %s\n", fork.Path)
	fmt.Printf("Parent disk:    %s (RAM-overlay, read-only)\n", vmPrimaryDiskPath(parentDir))
	emitMetricEvent("fork_created", forkStarted, "ok", map[string]any{
		"child_name": fork.Name,
		"child_path": fork.Path,
	})

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
	if cfg.Windows {
		runErr = runWindowsVMHook()
	} else if cfg.Linux {
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
