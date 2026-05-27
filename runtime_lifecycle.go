package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tmc/apple/dispatch"
	vz "github.com/tmc/apple/virtualization"

	"github.com/tmc/cove/internal/disposable"
	"github.com/tmc/cove/internal/lifecycle"
	"github.com/tmc/cove/internal/vmconfig"
	"github.com/tmc/cove/internal/vmpolicy"
	"github.com/tmc/cove/internal/vmrun"
)

type RunConfig struct {
	VM                         vmSelection
	Stdout                     io.Writer
	Stderr                     io.Writer
	Linux                      bool
	Windows                    bool
	Disposable                 bool
	RollbackSnapshot           string
	SetupRollbackSnapshotClone func(RollbackSnapshotCloneOptions) (disposable.Clone, error)
	Hooks                      RunHooks
	VMRun                      vmrun.RunConfig
	VMHost                     vmrun.HostConfig
	DisposableSourceDiskPath   string
	SystemDiskAttachment       systemDiskAttachmentMode
	SystemDiskPathOverride     string
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

type RunHooks struct {
	SetupDisposableClone             func(DisposableSetupOptions) (disposable.Clone, error)
	CleanupDisposableClone           func(string) error
	SetupEphemeralFork               func(EphemeralForkOptions) (EphemeralFork, error)
	CleanupEphemeralFork             func(string) error
	RunMacOSVM                       func(vmrun.RunConfig, vmrun.HostConfig, *RunBundle, runMetricRecorder) error
	RunLinuxVM                       func(vmrun.RunConfig, vmrun.HostConfig, *RunBundle, runMetricRecorder) error
	RunWindowsVM                     func(vmrun.RunConfig, vmrun.HostConfig, *RunBundle, runMetricRecorder) error
	StartPreparedFileHandleNetwork   func()
	StopPreparedFileHandleNetwork    func()
	ConfigureRequestedProxyAfterBoot func(*ControlServer)
	TeardownRequestedProxy           func(*ControlServer)
	AcquireRunLock                   func(string) (*RunLock, error)
	ConsumeRunBudget                 func(string, int) (int, error)
}

func defaultRunHooks() RunHooks {
	return RunHooks{
		SetupDisposableClone:             SetupDisposableClone,
		CleanupDisposableClone:           CleanupDisposableClone,
		SetupEphemeralFork:               SetupEphemeralFork,
		CleanupEphemeralFork:             CleanupEphemeralFork,
		RunMacOSVM:                       runMacOSVMWithConfig,
		RunLinuxVM:                       runLinuxVMWithConfig,
		RunWindowsVM:                     runWindowsVMWithConfig,
		StartPreparedFileHandleNetwork:   startPreparedFileHandleNetwork,
		StopPreparedFileHandleNetwork:    stopPreparedFileHandleNetwork,
		ConfigureRequestedProxyAfterBoot: configureRequestedProxyAfterBoot,
		TeardownRequestedProxy:           teardownRequestedProxy,
		AcquireRunLock:                   AcquireRunLock,
		ConsumeRunBudget:                 lifecycle.ConsumeRunBudget,
	}
}

func (h RunHooks) withDefaults() RunHooks {
	defaults := defaultRunHooks()
	if h.SetupDisposableClone == nil {
		h.SetupDisposableClone = defaults.SetupDisposableClone
	}
	if h.CleanupDisposableClone == nil {
		h.CleanupDisposableClone = defaults.CleanupDisposableClone
	}
	if h.SetupEphemeralFork == nil {
		h.SetupEphemeralFork = defaults.SetupEphemeralFork
	}
	if h.CleanupEphemeralFork == nil {
		h.CleanupEphemeralFork = defaults.CleanupEphemeralFork
	}
	if h.RunMacOSVM == nil {
		h.RunMacOSVM = defaults.RunMacOSVM
	}
	if h.RunLinuxVM == nil {
		h.RunLinuxVM = defaults.RunLinuxVM
	}
	if h.RunWindowsVM == nil {
		h.RunWindowsVM = defaults.RunWindowsVM
	}
	if h.StartPreparedFileHandleNetwork == nil {
		h.StartPreparedFileHandleNetwork = defaults.StartPreparedFileHandleNetwork
	}
	if h.StopPreparedFileHandleNetwork == nil {
		h.StopPreparedFileHandleNetwork = defaults.StopPreparedFileHandleNetwork
	}
	if h.ConfigureRequestedProxyAfterBoot == nil {
		h.ConfigureRequestedProxyAfterBoot = defaults.ConfigureRequestedProxyAfterBoot
	}
	if h.TeardownRequestedProxy == nil {
		h.TeardownRequestedProxy = defaults.TeardownRequestedProxy
	}
	if h.AcquireRunLock == nil {
		h.AcquireRunLock = defaults.AcquireRunLock
	}
	if h.ConsumeRunBudget == nil {
		h.ConsumeRunBudget = defaults.ConsumeRunBudget
	}
	return h
}

func currentRunConfig() RunConfig {
	return currentRuntimeOptions().runConfig()
}

func (opts runtimeOptions) runConfig() RunConfig {
	return RunConfig{
		VM:                         currentVMSelection(),
		Stdout:                     os.Stdout,
		Stderr:                     os.Stderr,
		Linux:                      opts.Linux,
		Windows:                    opts.Windows,
		Disposable:                 opts.Disposable,
		RollbackSnapshot:           opts.RollbackSnapshot,
		SetupRollbackSnapshotClone: SetupRollbackSnapshotClone,
		Hooks:                      defaultRunHooks(),
		VMRun:                      opts.vmrunRunConfig(opts.guestOS()),
		VMHost:                     opts.vmrunHostConfig(),
		DisposableSourceDiskPath:   opts.DisposableSourceDiskPath,
		SystemDiskAttachment:       opts.SystemDiskAttachment,
		SystemDiskPathOverride:     opts.SystemDiskPathOverride,
		EphemeralForkParent:        opts.EphemeralForkParent,
		EphemeralForkName:          opts.EphemeralForkName,
		EphemeralForkKeep:          opts.EphemeralForkKeep,
		Ephemeral:                  opts.Ephemeral,
	}
}

func currentRunConfigForEnv(env commandEnv) RunConfig {
	env = env.WithDefaultIO()
	cfg := currentRunConfig()
	cfg.Stdout = env.Stdout
	cfg.Stderr = env.Stderr
	return cfg
}

func runCurrentVM() error {
	return runVMWithConfig(currentRunConfig())
}

func runCurrentVMWithEnv(env commandEnv) error {
	return runVMWithConfig(currentRunConfigForEnv(env))
}

func runVMWithConfig(cfg RunConfig) error {
	if cfg.Stdout == nil {
		cfg.Stdout = os.Stdout
	}
	if cfg.Stderr == nil {
		cfg.Stderr = os.Stderr
	}
	hooks := cfg.Hooks.withDefaults()
	rc, hc := cfg.vmrunConfigs()
	originalVMName := cfg.VM.Name
	originalVMDir := cfg.VM.Directory

	if cfg.Disposable && cfg.RollbackSnapshot != "" {
		return fmt.Errorf("rollback snapshot runs already create a disposable clone")
	}
	if cfg.EphemeralForkParent != "" && (cfg.Disposable || cfg.RollbackSnapshot != "") {
		return fmt.Errorf("-fork-from is not compatible with -disposable or rollback snapshot runs")
	}

	if cfg.EphemeralForkParent != "" {
		if err := validateImageForkFromBeforeBundle(cfg.EphemeralForkParent); err != nil {
			return err
		}

		// Per-run artifact bundling is enabled only for the ephemeral
		// fork-from paths — these are short-lived jobs the operator
		// later wants to bisect. Plain `cove run <vm>` is a long-lived
		// workstation and is intentionally excluded.
		bundle, err := beginRunBundle(cfg)
		if err != nil {
			fmt.Fprintf(cfg.Stderr, "warning: run bundle init: %v\n", err)
		}
		writeActiveNetworkPolicyAudit(bundle)

		// If <ref> resolves to a local image (and not a VM name), take
		// the image-fork-from path: clonefile-materialize a fresh bundle
		// and boot it. Falls through to the legacy RAM-overlay path when
		// the ref is a VM name.
		var runErr error
		if isImageForkFromRef(cfg.EphemeralForkParent) {
			runErr = runImageForkFromWithConfig(cfg, originalVMName, originalVMDir, bundle)
		} else {
			runErr = runEphemeralForkWithConfig(cfg, originalVMName, originalVMDir, bundle)
		}
		finishRunBundle(bundle, runErr)
		return runErr
	}

	metricsRun, err := beginStandaloneMetricsRun(originalVMName, "", originalVMDir)
	if err != nil {
		fmt.Fprintf(cfg.Stderr, "warning: metrics init: %v\n", err)
	}
	writeActiveNetworkPolicyAudit(metricsRun)
	defer finishStandaloneMetricsRun(metricsRun)

	if err := enforceRunBudget(originalVMName, originalVMDir, metricsRun, hooks.ConsumeRunBudget); err != nil {
		if metricsRun != nil {
			metricsRun.EmitMetricEvent("run_complete", metricsRun.started, err.Error(), nil)
		}
		return err
	}

	var clone disposable.Clone
	temporaryClone := cfg.Disposable || cfg.RollbackSnapshot != ""
	if temporaryClone {
		source := originalVMName
		if source == "" {
			source = filepathBase(originalVMDir)
		}
		var (
			created disposable.Clone
			err     error
		)
		if cfg.RollbackSnapshot != "" {
			setupRollbackSnapshotClone := cfg.SetupRollbackSnapshotClone
			if setupRollbackSnapshotClone == nil {
				setupRollbackSnapshotClone = SetupRollbackSnapshotClone
			}
			created, err = setupRollbackSnapshotClone(RollbackSnapshotCloneOptions{
				Source:   source,
				Snapshot: cfg.RollbackSnapshot,
			})
		} else {
			created, err = hooks.SetupDisposableClone(DisposableSetupOptions{
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
			fmt.Fprintf(cfg.Stdout, "Rollback snapshot: %s\n", cfg.RollbackSnapshot)
			fmt.Fprintf(cfg.Stdout, "Rollback clone: %s\n", clone.Name)
			fmt.Fprintf(cfg.Stdout, "Rollback path: %s\n", clone.Path)
		} else {
			fmt.Fprintf(cfg.Stdout, "Disposable clone: %s\n", clone.Name)
			fmt.Fprintf(cfg.Stdout, "Disposable path: %s\n", clone.Path)
		}
		if cfg.SystemDiskAttachment == systemDiskAttachmentTemporaryRAM {
			fmt.Fprintf(cfg.Stdout, "System disk attachment: %s\n", cfg.SystemDiskAttachment)
		}
		defer func() {
			vmName = originalVMName
			vmDir = originalVMDir
		}()
	}

	lock, err := hooks.AcquireRunLock(vmDir)
	if err != nil {
		return fmt.Errorf("cove run: %w", err)
	}
	noteVMRuntimePhase(vmDir, "starting", "configuring")
	defer func() {
		if releaseErr := lock.Release(); releaseErr != nil {
			fmt.Fprintf(cfg.Stderr, "warning: release run.lock: %v\n", releaseErr)
		}
	}()

	var runErr error
	if cfg.Windows {
		runErr = hooks.RunWindowsVM(rc, hc, nil, metricsRun)
	} else if cfg.Linux {
		runErr = hooks.RunLinuxVM(rc, hc, nil, metricsRun)
	} else {
		runErr = hooks.RunMacOSVM(rc, hc, nil, metricsRun)
	}
	exit := "ok"
	if runErr != nil {
		exit = runErr.Error()
	}
	if metricsRun != nil {
		metricsRun.EmitResourceSampleMetric("end")
		metricsRun.EmitMetricEvent("run_complete", metricsRun.started, exit, nil)
	}

	if temporaryClone {
		if cleanupErr := hooks.CleanupDisposableClone(clone.Path); cleanupErr != nil {
			fmt.Fprintf(cfg.Stderr, "warning: cleanup disposable clone: %v\n", cleanupErr)
		} else if cfg.RollbackSnapshot != "" {
			fmt.Fprintf(cfg.Stdout, "Rollback clone removed: %s\n", clone.Name)
		} else {
			fmt.Fprintf(cfg.Stdout, "Disposable clone removed: %s\n", clone.Name)
		}
	}

	return runErr
}

func currentGuestOS() vmrun.GuestOS {
	return currentRuntimeOptions().guestOS()
}

func (opts runtimeOptions) guestOS() vmrun.GuestOS {
	switch {
	case opts.Windows:
		return vmrun.GuestWindows
	case opts.Linux:
		return vmrun.GuestLinux
	default:
		return vmrun.GuestMacOS
	}
}

func (cfg RunConfig) guestOS() vmrun.GuestOS {
	switch {
	case cfg.Windows:
		return vmrun.GuestWindows
	case cfg.Linux:
		return vmrun.GuestLinux
	default:
		return vmrun.GuestMacOS
	}
}

func (cfg RunConfig) vmrunConfigs() (vmrun.RunConfig, vmrun.HostConfig) {
	rc := cfg.VMRun
	if rc.OS == vmrun.GuestUnknown {
		rc = vmrunRunConfig(cfg.guestOS())
	}
	if rc.OS == vmrun.GuestUnknown {
		rc.OS = cfg.guestOS()
	}
	hc := cfg.VMHost
	if hc.VMDir == "" && hc.VMName == "" {
		hc = vmrunHostConfig()
	}
	if cfg.VM.Name != "" {
		hc.VMName = cfg.VM.Name
	}
	if cfg.VM.Directory != "" {
		hc.VMDir = cfg.VM.Directory
	}
	return rc, hc
}

func enforceRunBudget(vmName, vmDir string, metricsRun *standaloneMetricsRun, consumeRunBudget func(string, int) (int, error)) error {
	policy, err := vmpolicy.Load(vmDir)
	if err != nil {
		return err
	}
	if policy.RunBudget <= 0 {
		return nil
	}
	if consumeRunBudget == nil {
		consumeRunBudget = lifecycle.ConsumeRunBudget
	}
	runsUsed, err := consumeRunBudget(vmDir, policy.RunBudget)
	if errors.Is(err, lifecycle.ErrBudgetExceeded) {
		started := time.Time{}
		if metricsRun != nil {
			started = metricsRun.started
		}
		metricsRun.EmitMetricEvent("lifecycle.budget.exceeded", started, "exceeded", map[string]any{
			"vm_name":      vmName,
			"budget_count": policy.RunBudget,
			"runs_used":    runsUsed,
			"policy_path":  vmpolicy.Path(vmDir),
		})
		return fmt.Errorf("vm %q run budget exceeded: %w", vmName, err)
	}
	if err != nil {
		return fmt.Errorf("consume run budget: %w", err)
	}
	return nil
}

func lifecyclePolicyEventType(reason string) string {
	switch reason {
	case "idle":
		return "lifecycle.idle.tripped"
	case "max_age":
		return "lifecycle.maxage.tripped"
	default:
		return "vm_policy_stop"
	}
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
	startControlRuntimeInfrastructureWithHooks(controlServer, defaultRunHooks())
}

func startControlRuntimeInfrastructureWithHooks(controlServer *ControlServer, hooks RunHooks) {
	hooks = hooks.withDefaults()
	hooks.StartPreparedFileHandleNetwork()
	hooks.ConfigureRequestedProxyAfterBoot(controlServer)
	startVMLifecyclePolicyMonitor(controlServer)
}

func stopControlRuntimeInfrastructure(controlServer *ControlServer) {
	stopControlRuntimeInfrastructureWithHooks(controlServer, defaultRunHooks())
}

func stopControlRuntimeInfrastructureWithHooks(controlServer *ControlServer, hooks RunHooks) {
	hooks = hooks.withDefaults()
	hooks.TeardownRequestedProxy(controlServer)
	hooks.StopPreparedFileHandleNetwork()
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
	case policy.MaxAge > 0 && !startedAt.IsZero() && now.Sub(startedAt) >= policy.MaxAge:
		reason = "max_age"
	case policy.IdleTimeout > 0 && state == vz.VZVirtualMachineStateRunning:
		lastPing := s.bridge.LastPing()
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

	vmDir := s.effectiveVMDir()
	s.recordMetric(lifecyclePolicyEventType(reason), startedAt, "tripped", map[string]any{
		"reason":           reason,
		"vm_name":          filepathBase(vmDir),
		"policy_path":      vmpolicy.Path(vmDir),
		"idle_timeout_s":   int64(policy.IdleTimeout / time.Second),
		"max_age_s":        int64(policy.MaxAge / time.Second),
		"run_budget_count": policy.RunBudget,
		"runs_used":        execCount,
	})
	if err := lifecycleRequestStopHook(s); err != nil {
		fmt.Fprintf(os.Stderr, "warning: vm policy stop: %v\n", err)
	}
}

// runEphemeralForkWithConfig boots a Phase 3 ephemeral sibling: an
// in-memory child that shares the parent's disk.img read-only via
// VZTemporaryRAMStorageDeviceAttachment. The child's vmDir is
// auto-removed on exit unless cfg.EphemeralForkKeep is set.
func runEphemeralForkWithConfig(cfg RunConfig, originalVMName, originalVMDir string, bundle *RunBundle) error {
	hooks := cfg.Hooks.withDefaults()
	rc, hc := cfg.vmrunConfigs()
	parentDir := vmconfig.Path(cfg.EphemeralForkParent)
	if !vmconfig.Validate(parentDir) {
		return missingForkFromParentError(cfg.EphemeralForkParent)
	}

	// Probe-and-release the parent's run.lock. If we can't acquire
	// LOCK_EX, the parent is running and we refuse to attach to its
	// disk.img. Validation #1 showed VZ takes no file lock at attach
	// time, so this guard is enforced on our side.
	parentLock, err := hooks.AcquireRunLock(parentDir)
	if err != nil {
		if errors.Is(err, ErrRunLockHeld) {
			return fmt.Errorf("cove run -fork-from: parent VM %q is running; ephemeral fork requires parent stopped", cfg.EphemeralForkParent)
		}
		return fmt.Errorf("cove run -fork-from: probe parent run.lock: %w", err)
	}
	if releaseErr := parentLock.Release(); releaseErr != nil {
		fmt.Fprintf(cfg.Stderr, "warning: release parent run.lock: %v\n", releaseErr)
	}
	if err := validateEphemeralForkVMParent(cfg.EphemeralForkParent, parentDir); err != nil {
		return err
	}

	forkStarted := time.Now()
	fork, err := hooks.SetupEphemeralFork(EphemeralForkOptions{
		Parent:           cfg.EphemeralForkParent,
		Name:             cfg.EphemeralForkName,
		PreserveIdentity: true,
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(cfg.Stdout, "Ephemeral fork: %s\n", fork.Name)
	fmt.Fprintf(cfg.Stdout, "Ephemeral path: %s\n", fork.Path)
	fmt.Fprintf(cfg.Stdout, "Parent disk:    %s (RAM-overlay, read-only)\n", vmPrimaryDiskPath(parentDir))
	bundle.EmitMetricEvent("fork_created", forkStarted, "ok", map[string]any{
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

	lock, err := hooks.AcquireRunLock(vmDir)
	if err != nil {
		// Lock acquisition failed before booting; remove the dir so
		// no orphan is left behind (it won't have been used).
		_ = hooks.CleanupEphemeralFork(fork.Path)
		return fmt.Errorf("cove run -fork-from: %w", err)
	}
	defer func() {
		if releaseErr := lock.Release(); releaseErr != nil {
			fmt.Fprintf(cfg.Stderr, "warning: release run.lock: %v\n", releaseErr)
		}
	}()

	var runErr error
	if cfg.Windows {
		runErr = hooks.RunWindowsVM(rc, hc, bundle, bundle)
	} else if cfg.Linux {
		runErr = hooks.RunLinuxVM(rc, hc, bundle, bundle)
	} else {
		runErr = hooks.RunMacOSVM(rc, hc, bundle, bundle)
	}

	if cfg.EphemeralForkKeep {
		fmt.Fprintf(cfg.Stdout, "Ephemeral fork retained (-keep): %s\n", fork.Path)
		return runErr
	}
	if cleanupErr := hooks.CleanupEphemeralFork(fork.Path); cleanupErr != nil {
		fmt.Fprintf(cfg.Stderr, "warning: cleanup ephemeral fork: %v\n", cleanupErr)
	} else {
		fmt.Fprintf(cfg.Stdout, "Ephemeral fork removed: %s\n", fork.Name)
	}
	return runErr
}

func missingForkFromParentError(parent string) error {
	ref, err := ParseImageRef(parent)
	if err != nil {
		return fmt.Errorf("cove run -fork-from: no VM named %q under %s", parent, vmconfig.BaseDir())
	}
	return fmt.Errorf("cove run -fork-from: no VM named %q under %s and no local image %s; run 'cove image list' or 'cove image search %s' to find images, or use 'cove fork %s <child>' / 'cove clone --linked %s <child>' for VM parents", parent, vmconfig.BaseDir(), ref, ref.Name, parent, parent)
}

func validateEphemeralForkVMParent(parent, parentDir string) error {
	switch vmconfig.DetectOSType(parentDir) {
	case "Linux":
		return fmt.Errorf("cove run -fork-from: VM parent %q is Linux; VM-parent RAM-overlay forks are not implemented. Use 'cove fork %s <child>' or 'cove clone --linked %s <child>', then run the child VM", parent, parent, parent)
	case "Windows":
		return fmt.Errorf("cove run -fork-from: VM parent %q is Windows; VM-parent RAM-overlay forks are not implemented. Use 'cove fork %s <child>' or 'cove clone --linked %s <child>', then run the child VM", parent, parent, parent)
	default:
		return fmt.Errorf("cove run -fork-from: VM parent %q uses the RAM-overlay runtime, which is not implemented. No VM was created. Use 'cove fork %s <child>' or 'cove clone --linked %s <child>', then run the child VM; image refs still work with -fork-from", parent, parent, parent)
	}
}
