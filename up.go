// up.go — Single command to install, provision, and optionally run vzscripts.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tmc/vz-macos/internal/controlserver"
	"github.com/tmc/vz-macos/internal/vmconfig"
	controlpb "github.com/tmc/vz-macos/proto/controlpb"
	"golang.org/x/tools/txtar"
)

// upConfig holds all configuration for the "up" command.
// It is built from flags and used to drive the install/inject/run pipeline.
type upConfig struct {
	user                     string
	password                 string
	vmName                   string
	vmDir                    string
	ipswPath                 string
	vzscripts                string
	setupScriptPath          string
	cpuCount                 uint
	memoryGB                 uint64
	diskSizeGB               uint64
	automationBackend        string
	automationCaptureBackend string
	automationInputBackend   string
	gui                      bool
	force                    bool
	noShutdown               bool
	verbose                  bool
	linux                    bool
	desktop                  bool
	desktopInstaller         string
	diskSync                 string
	distro                   string
	nested                   bool
	nvme                     bool
	cpuExplicit              bool
	rosetta                  bool
	networkMode              string
	portForwards             portForwardSpecs
}

// handleUp implements the "up" subcommand: install -> inject -> run -> vzscripts.
func handleUp(args []string) error {
	cfg, err := parseUpFlags(args)
	if err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	// Apply configuration to package-level state.
	// The install/inject/run functions read these globals directly.
	// This is the single point where "up" touches global state.
	applyUpConfig(cfg)
	maybeStartPprofServer()

	if err := runUpPipeline(cfg); err != nil {
		return err
	}
	return nil
}

// parseUpFlags parses flags and prompts for missing values.
// Returns a fully populated upConfig or an error.
func parseUpFlags(args []string) (upConfig, error) {
	fs, cfg, headless := newUpFlagSet()

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return upConfig{}, err
		}
		return upConfig{}, err
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "cpu" {
			cfg.cpuExplicit = true
		}
	})
	// -desktop, -nested, and -nvme imply -linux.
	if cfg.desktop || cfg.nested || cfg.nvme {
		cfg.linux = true
	}
	variant, err := parseLinuxVariant(cfg.distro, cfg.desktop)
	if err != nil {
		return upConfig{}, err
	}
	if cfg.nested && !cfg.cpuExplicit && cfg.cpuCount < 4 {
		cfg.cpuCount = 4
	}
	// For Linux, user is optional.
	if cfg.user == "" && !cfg.linux {
		return upConfig{}, fmt.Errorf("missing required flag: -user")
	}
	if cfg.linux && cfg.user == "" {
		cfg.user = defaultLinuxUser(variant)
	}
	if *headless {
		cfg.gui = false
	}
	if _, err := parseAutomationBackend(cfg.automationBackend); err != nil {
		return upConfig{}, err
	}
	if strings.TrimSpace(cfg.automationCaptureBackend) != "" {
		if _, err := parseAutomationCaptureBackend(cfg.automationCaptureBackend); err != nil {
			return upConfig{}, err
		}
	}
	if strings.TrimSpace(cfg.automationInputBackend) != "" {
		if _, err := parseAutomationInputBackend(cfg.automationInputBackend); err != nil {
			return upConfig{}, err
		}
	}
	if err := validateNetworkMode(cfg.networkMode); err != nil {
		return upConfig{}, err
	}

	// Validate setup script is readable before doing any heavy work.
	if cfg.setupScriptPath != "" {
		if _, err := os.Stat(cfg.setupScriptPath); err != nil {
			return upConfig{}, fmt.Errorf("setup-script: %w", err)
		}
	}

	if cfg.vmName == "" {
		cfg.vmName = vmName
	}
	vzlog("parseUpFlags: resolving cfg.vmName=%q with global vmDir=%q", cfg.vmName, vmDir)
	dir, err := vmconfig.EnsureDir(cfg.vmName, vmDir)
	if err != nil {
		return upConfig{}, err
	}
	cfg.vmDir = dir
	vzlog("parseUpFlags: resolved cfg.vmDir=%q", cfg.vmDir)

	// Prompt for password if not provided (skip for Linux with defaults).
	if cfg.password == "" && !cfg.linux {
		preferPasswordDialog = cfg.gui
		pw, err := readPassword(fmt.Sprintf("Password for %s: ", cfg.user))
		if err != nil {
			return upConfig{}, fmt.Errorf("read password: %w", err)
		}
		cfg.password = string(pw)
		if cfg.password == "" {
			return upConfig{}, fmt.Errorf("password required")
		}
	} else if cfg.password == "" && cfg.linux {
		cfg.password = cfg.user // Default: password = username for Linux.
	}

	return *cfg, nil
}

func newUpFlagSet() (*flag.FlagSet, *upConfig, *bool) {
	fs := flag.NewFlagSet("up", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cfg := &upConfig{}
	headless := new(bool)

	fs.StringVar(&cfg.user, "user", "", "Username for the provisioned user (required)")
	fs.StringVar(&cfg.password, "password", "", "Password for the provisioned user (prompts if empty)")
	fs.StringVar(&cfg.vzscripts, "vzscripts", "", "Comma-separated vzscript recipes to run after boot (e.g. homebrew,openclaw)")
	fs.StringVar(&cfg.setupScriptPath, "setup-script", "", "Path to a plain text script: each non-blank, non-# line runs via the guest agent after boot")
	fs.StringVar(&cfg.ipswPath, "ipsw", "", "Path to IPSW restore image (downloads latest if empty)")
	fs.BoolVar(&cfg.force, "force", false, "Force install even if the VM disk already exists")
	fs.BoolVar(&cfg.gui, "gui", true, "Show VM display in a window")
	fs.BoolVar(headless, "headless", false, "Run without a GUI window")
	fs.UintVar(&cfg.cpuCount, "cpu", 2, "Number of CPUs")
	fs.Uint64Var(&cfg.memoryGB, "memory", 4, "Memory in GB")
	fs.Uint64Var(&cfg.diskSizeGB, "disk-size", 64, "Disk size in GB")
	fs.StringVar(&cfg.automationBackend, "automation-backend", "auto", "UI automation backend: auto, framebuffer, or window")
	fs.StringVar(&cfg.automationCaptureBackend, "automation-capture-backend", "", "override screenshot backend: auto, framebuffer, or window")
	fs.StringVar(&cfg.automationInputBackend, "automation-input-backend", "", "override input backend: auto, direct, or window")
	fs.BoolVar(&cfg.noShutdown, "no-shutdown", false, "Leave the VM running after vzscripts complete")
	fs.BoolVar(&cfg.verbose, "v", false, "Verbose output")
	fs.StringVar(&pprofAddr, "pprof", "", "serve net/http/pprof on localhost for diagnostics (for example 6060 or localhost:6060)")
	fs.StringVar(&cfg.vmName, "vm", "", "VM name (default: active VM or 'default')")
	fs.BoolVar(&cfg.linux, "linux", false, "Install a Linux VM instead of macOS")
	fs.BoolVar(&cfg.desktop, "desktop", false, "Use Ubuntu Desktop ISO (implies -linux)")
	fs.StringVar(&cfg.desktopInstaller, "desktop-installer", "oem", "ubuntu desktop install path: 'oem' (default Desktop ISO autoinstall) or 'server' (boot Server ISO + apt install ubuntu-desktop)")
	fs.StringVar(&cfg.diskSync, "disk-sync", "", "disk image synchronization override: fsync, none, or full")
	fs.StringVar(&cfg.distro, "distro", "ubuntu", "Linux distro: ubuntu, debian, fedora, alpine")
	fs.BoolVar(&cfg.nested, "nested", false, "Enable nested virtualization for Linux guests (M3/M4 on macOS 15+)")
	fs.BoolVar(&cfg.nvme, "nvme", false, "Attach Linux root disk through NVMe instead of virtio-blk")
	fs.BoolVar(&cfg.rosetta, "rosetta", true, "Enable Rosetta translation support for Linux VMs")
	fs.StringVar(&cfg.networkMode, "network", "nat", "network mode: nat, bridged:<iface>, host-only, none")
	fs.StringVar(&cfg.networkMode, "net", "nat", "alias for -network")
	fs.Var(&cfg.portForwards, "port-forward", "forward host TCP to guest vsock: hostPort:guestVsockPort (repeatable)")
	fs.Var(&cfg.portForwards, "pf", "alias for -port-forward")
	fs.Usage = func() {
		printUpUsage(os.Stderr, fs)
	}
	return fs, cfg, headless
}

func printUpUsage(w io.Writer, fs *flag.FlagSet) {
	fmt.Fprintf(w, `Usage: cove up [options]

Install, provision, and boot a VM in one command.

macOS: install -> provision -> run [-> vzscripts]
Linux: install (cloud-init provisions) -> run [-> vzscripts]

Options:
`)
	fs.VisitAll(func(f *flag.Flag) {
		if f.Name == "disk-sync" {
			return
		}
		if f.Name == "nvme" {
			return
		}
		fmt.Fprintf(w, "  -%s", f.Name)
		if f.DefValue != "false" && f.DefValue != "" {
			fmt.Fprintf(w, " %s", f.DefValue)
		}
		if f.Usage != "" {
			fmt.Fprintf(w, "\n    \t%s", f.Usage)
		}
		if f.DefValue != "" && f.DefValue != "false" {
			fmt.Fprintf(w, " (default %q)", f.DefValue)
		}
		fmt.Fprintln(w)
	})
	fmt.Fprintf(w, `
Examples:
  cove up -user me                                        # macOS VM
  cove up -user me -vzscripts homebrew,openclaw            # macOS + recipes
  cove up -user me -setup-script ./setup.sh                # macOS + plain script
  cove up -user me -ipsw ~/restore.ipsw                   # macOS with IPSW
  cove up -linux                                           # Ubuntu Server (ubuntu/ubuntu)
  cove up -linux -distro alpine                            # Alpine (alpine/alpine)
  cove up -linux -nested                                   # Linux with KVM on supported hosts
  cove up -linux -user tmc -password secret                # Linux with custom user
  cove up -linux -desktop -user me                         # Ubuntu Desktop
  cove up -linux -headless -cpu 4 -memory 8                # Headless Linux Server
`)
}

// applyUpConfig sets the package-level globals that installMacOSLikeVZ,
// stageProvisioningFiles, applyProvisioningFiles, and runMacOSVM read.
func applyUpConfig(cfg upConfig) {
	vzlog("applyUpConfig: cfg.vmName=%q cfg.vmDir=%q (pre globals: vmDir=%q vmName=%q)", cfg.vmName, cfg.vmDir, vmDir, vmName)
	vmName = cfg.vmName
	vmDir = cfg.vmDir
	vzlog("applyUpConfig: post globals: vmDir=%q vmName=%q", vmDir, vmName)
	if cfg.ipswPath != "" {
		ipswPath = cfg.ipswPath
	}
	forceInstall = cfg.force
	cpuCount = cfg.cpuCount
	memoryGB = cfg.memoryGB
	diskSizeGB = cfg.diskSizeGB
	verbose = cfg.verbose
	controlserver.Verbose = cfg.verbose
	guiMode = cfg.gui
	automationBackend = cfg.automationBackend
	automationCaptureBackend = cfg.automationCaptureBackend
	automationInputBackend = cfg.automationInputBackend
	provisionUser = cfg.user
	provisionPassword = cfg.password
	provisionStrategy = "disk"
	installVM = true
	if cfg.linux {
		linuxMode = true
	}
	enableRosetta = cfg.rosetta
	if cfg.desktop {
		linuxDesktop = true
	}
	if cfg.desktopInstaller != "" {
		linuxDesktopInstaller = cfg.desktopInstaller
	}
	diskSyncMode = cfg.diskSync
	if cfg.distro != "" {
		linuxDistro = cfg.distro
	}
	if cfg.nested {
		linuxNested = true
	}
	if cfg.nvme {
		linuxNVMe = true
	}
	cpuExplicit = cfg.cpuExplicit
	networkMode = cfg.networkMode
	startupPortForwards = cfg.portForwards
}

// vmAlreadyInstalled reports whether the VM disk already exists, meaning
// a previous install completed. When true, up can skip directly to boot.
func vmAlreadyInstalled(dir string, linux bool) bool {
	if linux {
		if _, ok := loadInstalledLinuxBootArtifacts(dir); ok {
			return true
		}
		info, err := os.Stat(linuxInstalledMarkerPath(dir))
		return err == nil && info.Size() > 0
	}
	name := "disk.img"
	info, err := os.Stat(filepath.Join(dir, name))
	return err == nil && info.Size() > 0
}

// runUpPipeline executes the install -> inject -> run pipeline.
// If the VM is already installed (disk exists) and -force is not set,
// it skips install and provisioning and proceeds to boot + vzscripts.
func runUpPipeline(cfg upConfig) (err error) {
	target := currentVMSelection()
	metricsRun, metricsErr := beginStandaloneMetricsRun(target.Name, "")
	if metricsErr != nil {
		fmt.Fprintf(os.Stderr, "warning: metrics init: %v\n", metricsErr)
	}
	defer finishStandaloneMetricsRun(metricsRun)
	defer func(started time.Time) {
		if metricsRun != nil {
			status := "ok"
			if err != nil {
				status = err.Error()
			}
			emitMetricEvent("run_complete", started, status, map[string]any{"command": "up"})
		}
	}(time.Now())
	if cfg.linux {
		return runLinuxUpPipeline(cfg)
	}

	installed := vmAlreadyInstalled(target.Directory, false)
	if err := requireRootForMacOSUpProvisioning(cfg, target, installed); err != nil {
		return err
	}

	// Step 1: Install macOS (skip if already installed).
	if installed && !cfg.force {
		fmt.Println("=== Step 1/3: Install (already done) ===")
	} else {
		fmt.Println("=== Step 1/3: Installing macOS ===")
		createStarted := time.Now()
		installErr := installMacOSLikeVZ(context.Background())
		if installErr != nil && !errors.Is(installErr, errRestartVM) {
			emitMetricEvent("vm_create", createStarted, installErr.Error(), map[string]any{"command": "up"})
			return fmt.Errorf("install: %w", installErr)
		}
		emitMetricEvent("vm_create", createStarted, "ok", map[string]any{"command": "up"})
	}

	// Step 2: Inject provisioning files.
	// The install step may have already injected (via stopVMAndInject) in
	// headless mode and stamped the marker. If the marker is missing, the
	// inject was either never attempted or failed mid-flight — retry it
	// either way. The staging dir is reused if present (stageProvisioningFiles
	// is idempotent).
	verifyProvisionedUser := ""
	if !didInjectSucceedForVM(target) {
		fmt.Println()
		fmt.Println("=== Step 2/3: Provisioning VM ===")
		opts := InjectOptions{
			Config: ProvisionConfig{
				Username:          cfg.user,
				Password:          cfg.password,
				Admin:             true,
				BootstrapRecovery: true,
			},
			SkipSetupAssistant: true,
			AutoLogin:          true,
			InjectAgent:        sandboxAllowsAgentProvision(),
			InjectGuestTools:   !sandboxActive(),
		}
		if _, err := stageProvisioningFilesForVM(target, opts); err != nil {
			return fmt.Errorf("stage provisioning: %w", err)
		}
		// Stage inject directives from vzscript recipes before applying.
		if cfg.vzscripts != "" {
			if err := stageVZScriptInjectsForVM(target, splitRecipes(cfg.vzscripts)); err != nil {
				return fmt.Errorf("stage vzscript injects: %w", err)
			}
		}
		if err := applyProvisioningFilesForVM(target); err != nil {
			return fmt.Errorf("apply provisioning: %w", err)
		}
		verifyProvisionedUser = cfg.user
	} else {
		fmt.Println()
		fmt.Println("=== Step 2/3: Provisioning (already done) ===")
	}

	// Step 3: Boot VM and optionally run vzscripts and/or setup-script.
	recipes := splitRecipes(cfg.vzscripts)
	if len(recipes) > 0 || cfg.setupScriptPath != "" {
		if len(recipes) > 0 {
			savePostInstallRecipes(target.Directory, cfg.vzscripts)
		}
		fmt.Println()
		switch {
		case len(recipes) > 0 && cfg.setupScriptPath != "":
			fmt.Printf("=== Step 3/3: Boot + vzscripts (%s) + setup-script (%s) ===\n", cfg.vzscripts, cfg.setupScriptPath)
		case len(recipes) > 0:
			fmt.Printf("=== Step 3/3: Boot + vzscripts (%s) ===\n", cfg.vzscripts)
		default:
			fmt.Printf("=== Step 3/3: Boot + setup-script (%s) ===\n", cfg.setupScriptPath)
		}
		return runUpWithVZScripts(recipes, cfg.setupScriptPath, cfg.noShutdown, cfg.verbose, verifyProvisionedUser)
	}

	fmt.Println()
	fmt.Println("=== Step 3/3: Booting VM ===")
	return runUpMacOSVM(verifyProvisionedUser, cfg.verbose)
}

var upEffectiveUID = os.Geteuid

func requireRootForMacOSUpProvisioning(cfg upConfig, target vmSelection, installed bool) error {
	if cfg.linux || upEffectiveUID() == 0 {
		return nil
	}
	if installed && didInjectSucceedForVM(target) {
		return nil
	}
	if restrictedEnvironment() {
		return fmt.Errorf("auto-login provisioning needs the native macOS admin dialog; re-run cove up from a normal terminal")
	}
	return nil
}

// runUpWithVZScripts boots the VM in a goroutine, runs the given vzscript
// recipes followed by an optional plain setup-script, and either shuts down
// the VM or leaves it running based on noShutdown. If the VM exits
// unexpectedly during script execution, the error is returned immediately.
func runUpWithVZScripts(recipes []string, setupScript string, noShutdown, verboseMode bool, verifyUser string) error {
	if err := validateVZScriptRecipes(recipes); err != nil {
		return err
	}

	sock := GetControlSocketPath()
	cfg := vzscriptConfig{
		socketPath:  sock,
		execTimeout: 30 * time.Minute,
		verbose:     verboseMode,
		env: []string{
			"USERNAME=" + provisionUser,
			"PASSWORD=" + provisionPassword,
		},
	}

	// Run vzscripts in a goroutine. The VM must run on the main thread
	// because runMacOSVM does AppKit operations (NSWindow, VZVirtualMachineView)
	// that require the main OS thread.
	scriptsDone := make(chan error, 1)
	go func() {
		// Wait for the guest agent.
		fmt.Println("Waiting for VM to boot and guest agent...")
		waitScript := []byte("guest-wait 15m\n")
		if err := runVZScript(waitScript, "wait-for-agent", cfg); err != nil {
			scriptsDone <- fmt.Errorf("wait-for-agent: %w", err)
			return
		}
		emitAgentReadyMetric()
		if err := verifyProvisioningForUp(sock, verifyUser); err != nil {
			_, _ = ctlSendRequest(sock, &controlpb.ControlRequest{Type: "agent-shutdown"}, 30*time.Second, "agent-shutdown")
			scriptsDone <- err
			return
		}

		if len(recipes) > 0 {
			fmt.Printf("\n=== Running vzscripts: %s ===\n", strings.Join(recipes, ", "))
			if err := runVZScriptWithDeps(recipes, cfg); err != nil {
				scriptsDone <- err
				return
			}
			fmt.Println("=== Done: vzscripts ===")
		}

		if setupScript != "" {
			fmt.Printf("\n=== Running setup-script: %s ===\n", setupScript)
			if err := runSetupScript(setupScript); err != nil {
				scriptsDone <- err
				return
			}
			fmt.Printf("=== Done: setup-script ===\n")
		}

		if noShutdown {
			fmt.Println("\nAll post-boot scripts complete. VM is still running.")
			fmt.Println("Use 'cove ctl stop' to shut it down.")
			scriptsDone <- nil
			return
		}

		// Shut down the VM gracefully.
		fmt.Println("\nShutting down VM...")
		_, _ = ctlSendRequest(sock, &controlpb.ControlRequest{Type: "agent-shutdown"}, 30*time.Second, "agent-shutdown")
		fmt.Println("\nPost-install complete.")
		scriptsDone <- nil
	}()

	// Run the VM on the main thread (required for AppKit).
	// This blocks until the VM exits (user closes window, shutdown, or crash).
	vmErr := runMacOSVM()

	// Check if scripts reported an error before VM exited.
	select {
	case err := <-scriptsDone:
		if err != nil {
			return err
		}
	default:
	}

	return vmErr
}

func runUpMacOSVM(verifyUser string, verboseMode bool) error {
	if strings.TrimSpace(verifyUser) == "" {
		return runMacOSVM()
	}
	sock := GetControlSocketPath()
	cfg := vzscriptConfig{
		socketPath:  sock,
		execTimeout: 30 * time.Minute,
		verbose:     verboseMode,
	}
	verifyDone := make(chan error, 1)
	go func() {
		fmt.Println("Waiting for VM to boot and guest agent...")
		waitScript := []byte("guest-wait 15m\n")
		if err := runVZScript(waitScript, "wait-for-agent", cfg); err != nil {
			verifyDone <- fmt.Errorf("wait-for-agent: %w", err)
			return
		}
		if err := verifyProvisioningForUp(sock, verifyUser); err != nil {
			_, _ = ctlSendRequest(sock, &controlpb.ControlRequest{Type: "agent-shutdown"}, 30*time.Second, "agent-shutdown")
			verifyDone <- err
			return
		}
		verifyDone <- nil
	}()

	vmErr := runMacOSVM()
	select {
	case err := <-verifyDone:
		if err != nil {
			return err
		}
	default:
	}
	return vmErr
}

func verifyProvisioningForUp(sock, user string) error {
	if strings.TrimSpace(user) == "" {
		return nil
	}
	client := NewControlClient(sock)
	info, err := verifyProvisionedGuestUser(client, user)
	if err != nil {
		return err
	}
	fmt.Printf("Provisioning verified: user %s uid=%d home=%s\n", user, info.UID, info.Home)
	return nil
}

// runLinuxUpPipeline executes the install -> run pipeline for Linux VMs.
// Cloud-init handles user provisioning during install, so no inject step is needed.
func runLinuxUpPipeline(cfg upConfig) error {
	target := currentVMSelection()
	installed := vmAlreadyInstalled(target.Directory, true)

	// Step 1: Install Linux VM (skip if already installed).
	if installed && !cfg.force {
		fmt.Println("=== Step 1/2: Install (already done) ===")
	} else {
		fmt.Println("=== Step 1/2: Installing Linux VM ===")
		createStarted := time.Now()
		if err := installLinuxVM(); err != nil {
			emitMetricEvent("vm_create", createStarted, err.Error(), map[string]any{"command": "up", "guest_os": "linux"})
			return fmt.Errorf("install: %w", err)
		}
		emitMetricEvent("vm_create", createStarted, "ok", map[string]any{"command": "up", "guest_os": "linux"})
	}

	// Step 2: Boot VM and optionally run vzscripts and/or setup-script.
	recipes := splitRecipes(cfg.vzscripts)
	if len(recipes) > 0 || cfg.setupScriptPath != "" {
		if len(recipes) > 0 {
			savePostInstallRecipes(target.Directory, cfg.vzscripts)
		}
		fmt.Println()
		switch {
		case len(recipes) > 0 && cfg.setupScriptPath != "":
			fmt.Printf("=== Step 2/2: Boot + vzscripts (%s) + setup-script (%s) ===\n", cfg.vzscripts, cfg.setupScriptPath)
		case len(recipes) > 0:
			fmt.Printf("=== Step 2/2: Boot + vzscripts (%s) ===\n", cfg.vzscripts)
		default:
			fmt.Printf("=== Step 2/2: Boot + setup-script (%s) ===\n", cfg.setupScriptPath)
		}
		return runLinuxUpWithVZScripts(recipes, cfg.setupScriptPath, cfg.noShutdown, cfg.verbose)
	}

	fmt.Println()
	fmt.Println("=== Step 2/2: Booting Linux VM ===")
	return runLinuxVM()
}

// runLinuxUpWithVZScripts boots a Linux VM, runs vzscript recipes and an
// optional plain setup-script, then shuts down (unless noShutdown).
func runLinuxUpWithVZScripts(recipes []string, setupScript string, noShutdown, verboseMode bool) error {
	if err := validateVZScriptRecipes(recipes); err != nil {
		return err
	}

	sock := GetControlSocketPath()
	cfg := vzscriptConfig{
		socketPath:  sock,
		execTimeout: 30 * time.Minute,
		verbose:     verboseMode,
		env: []string{
			"USERNAME=" + provisionUser,
			"PASSWORD=" + provisionPassword,
		},
	}

	scriptsDone := make(chan error, 1)
	go func() {
		fmt.Println("Waiting for VM to boot and guest agent...")
		waitScript := []byte("guest-wait 15m\n")
		if err := runVZScript(waitScript, "wait-for-agent", cfg); err != nil {
			scriptsDone <- fmt.Errorf("wait-for-agent: %w", err)
			return
		}
		emitAgentReadyMetric()

		if len(recipes) > 0 {
			fmt.Printf("\n=== Running vzscripts: %s ===\n", strings.Join(recipes, ", "))
			if err := runVZScriptWithDeps(recipes, cfg); err != nil {
				scriptsDone <- err
				return
			}
			fmt.Println("=== Done: vzscripts ===")
		}

		if setupScript != "" {
			fmt.Printf("\n=== Running setup-script: %s ===\n", setupScript)
			if err := runSetupScript(setupScript); err != nil {
				scriptsDone <- err
				return
			}
			fmt.Printf("=== Done: setup-script ===\n")
		}

		if noShutdown {
			fmt.Println("\nAll post-boot scripts complete. VM is still running.")
			fmt.Println("Use 'cove ctl stop' to shut it down.")
			scriptsDone <- nil
			return
		}

		fmt.Println("\nShutting down VM...")
		_, _ = ctlSendRequest(sock, &controlpb.ControlRequest{Type: "agent-shutdown"}, 30*time.Second, "agent-shutdown")
		fmt.Println("\nPost-install complete.")
		scriptsDone <- nil
	}()

	vmErr := runLinuxVM()

	select {
	case err := <-scriptsDone:
		if err != nil && vmErr == nil {
			return err
		}
	default:
	}

	return vmErr
}

// runVZScriptOrVMErr runs a vzscript while also monitoring the VM error
// channel. If the VM exits before the script completes, the VM error is
// returned. This prevents scripts from hanging if the VM crashes during boot.
func runVZScriptOrVMErr(script []byte, name string, cfg vzscriptConfig, vmErr <-chan error) error {
	scriptErr := make(chan error, 1)
	go func() {
		scriptErr <- runVZScript(script, name, cfg)
	}()
	select {
	case err := <-scriptErr:
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		return nil
	case err := <-vmErr:
		if err != nil {
			return fmt.Errorf("vm exited during %s: %w", name, err)
		}
		return fmt.Errorf("vm exited unexpectedly during %s", name)
	}
}

// splitRecipes splits a comma-separated recipe list, trimming whitespace.
func splitRecipes(s string) []string {
	var names []string
	for _, name := range strings.Split(s, ",") {
		name = strings.TrimSpace(name)
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

func validateVZScriptRecipes(recipes []string) error {
	seen := map[string]bool{}
	inProgress := map[string]bool{}

	var walk func(name, requiredBy string) error
	walk = func(name, requiredBy string) error {
		if seen[name] {
			return nil
		}
		if inProgress[name] {
			return fmt.Errorf("dependency cycle detected at %s", name)
		}

		data, err := loadVZScriptData(name)
		if err != nil {
			if requiredBy != "" {
				return fmt.Errorf("dependency %q (required by %s) cannot be resolved: %w", name, requiredBy, err)
			}
			return fmt.Errorf("recipe %q: %w", name, err)
		}

		inProgress[name] = true
		defer delete(inProgress, name)

		meta := parseScriptMeta(txtar.Parse(data).Comment)
		for _, dep := range meta.requires {
			if err := walk(dep, name); err != nil {
				return err
			}
		}

		seen[name] = true
		return nil
	}

	for _, name := range recipes {
		if err := walk(name, ""); err != nil {
			return err
		}
	}
	return nil
}

// stageVZScriptInjectsForVM loads the named vzscript recipes and their transitive
// dependencies, extracts any # inject: directives from their metadata, and
// stages the referenced txtar files into the existing provisioning staging
// directory. This runs between stageProvisioningFilesForVM and applyProvisioningFilesForVM
// so the inject files are included in the same disk-write pass.
func stageVZScriptInjectsForVM(target vmSelection, recipes []string) error {
	stagingDir := provisionStagingDirForVM(target)
	manifest, err := readManifest(stagingDir)
	if err != nil {
		return fmt.Errorf("read staging manifest: %w", err)
	}

	staged := 0
	seen := map[string]bool{}

	// walk recursively resolves dependencies and stages inject directives.
	var walk func(name string) error
	walk = func(name string) error {
		if seen[name] {
			return nil
		}
		seen[name] = true

		data, err := loadVZScriptData(name)
		if err != nil {
			return fmt.Errorf("load recipe %s: %w", name, err)
		}
		ar := txtar.Parse(data)
		meta := parseScriptMeta(ar.Comment)

		// Resolve dependencies first (depth-first).
		for _, dep := range meta.requires {
			if err := walk(dep); err != nil {
				return err
			}
		}

		if len(meta.inject) == 0 {
			return nil
		}

		// Index txtar files by name for lookup.
		fileIndex := map[string][]byte{}
		for _, f := range ar.Files {
			fileIndex[f.Name] = f.Data
		}

		for _, inj := range meta.inject {
			content, ok := fileIndex[inj.txtarFile]
			if !ok {
				return fmt.Errorf("recipe %s: inject references txtar file %q, but it is not in the archive", name, inj.txtarFile)
			}
			mode := parseFileMode(inj.mode, 0644)
			if err := stageFile(stagingDir, inj.guestPath, content, mode, inj.owner, manifest); err != nil {
				return fmt.Errorf("recipe %s: stage inject %s: %w", name, inj.guestPath, err)
			}
			staged++
		}
		return nil
	}

	for _, name := range recipes {
		if err := walk(name); err != nil {
			return err
		}
	}

	if staged > 0 {
		if err := writeManifest(stagingDir, manifest); err != nil {
			return fmt.Errorf("update manifest: %w", err)
		}
		fmt.Printf("Staged %d file(s) from vzscript inject directives\n", staged)
	}
	return nil
}

// parseFileMode parses an octal mode string like "0755". Returns defaultMode
// if s is empty or unparseable.
func parseFileMode(s string, defaultMode os.FileMode) os.FileMode {
	if s == "" {
		return defaultMode
	}
	v, err := strconv.ParseUint(s, 8, 32)
	if err != nil {
		return defaultMode
	}
	return os.FileMode(v)
}
