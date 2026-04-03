// up.go — Single command to install, provision, and optionally run vzscripts.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

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
	// -desktop implies -linux.
	if cfg.desktop {
		cfg.linux = true
	}
	// For Linux, user is optional (defaults to ubuntu/ubuntu).
	if cfg.user == "" && !cfg.linux {
		return upConfig{}, fmt.Errorf("missing required flag: -user")
	}
	if cfg.linux && cfg.user == "" {
		cfg.user = "ubuntu"
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

	// Resolve VM directory.
	if cfg.vmName != "" {
		dir, err := EnsureVMDir(cfg.vmName)
		if err != nil {
			return upConfig{}, err
		}
		cfg.vmDir = dir
	}

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
	fs.StringVar(&cfg.vmName, "vm", "", "VM name (default: active VM or 'default')")
	fs.BoolVar(&cfg.linux, "linux", false, "Install a Linux VM instead of macOS")
	fs.BoolVar(&cfg.desktop, "desktop", false, "Use Ubuntu Desktop ISO (implies -linux)")
	fs.Usage = func() {
		printUpUsage(os.Stderr, fs)
	}
	return fs, cfg, headless
}

func printUpUsage(w io.Writer, fs *flag.FlagSet) {
	fmt.Fprintf(w, `Usage: vz-macos up [options]

Install, provision, and boot a VM in one command.

macOS: install -> provision -> run [-> vzscripts]
Linux: install (cloud-init provisions) -> run [-> vzscripts]

Options:
`)
	fs.PrintDefaults()
	fmt.Fprintf(w, `
Examples:
  vz-macos up -user me                                        # macOS VM
  vz-macos up -user me -vzscripts homebrew,openclaw            # macOS + recipes
  vz-macos up -user me -ipsw ~/restore.ipsw                   # macOS with IPSW
  vz-macos up -linux                                           # Linux Server (ubuntu/ubuntu)
  vz-macos up -linux -user tmc -password secret                # Linux with custom user
  vz-macos up -linux -desktop -user me                         # Ubuntu Desktop
  vz-macos up -linux -headless -cpu 4 -memory 8                # Headless Linux Server
`)
}

// applyUpConfig sets the package-level globals that installMacOSLikeVZ,
// stageProvisioningFiles, applyProvisioningFiles, and runMacOSVM read.
func applyUpConfig(cfg upConfig) {
	if cfg.vmName != "" {
		vmName = cfg.vmName
		vmDir = cfg.vmDir
	}
	if cfg.ipswPath != "" {
		ipswPath = cfg.ipswPath
	}
	forceInstall = cfg.force
	cpuCount = cfg.cpuCount
	memoryGB = cfg.memoryGB
	diskSizeGB = cfg.diskSizeGB
	verbose = cfg.verbose
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
	if cfg.desktop {
		linuxDesktop = true
	}
}

// runUpPipeline executes the install -> inject -> run pipeline.
func runUpPipeline(cfg upConfig) error {
	if cfg.linux {
		return runLinuxUpPipeline(cfg)
	}

	// Step 1: Install macOS.
	fmt.Println("=== Step 1/3: Installing macOS ===")
	installErr := installMacOSLikeVZ(context.Background())
	if installErr != nil && !errors.Is(installErr, errRestartVM) {
		return fmt.Errorf("install: %w", installErr)
	}

	// Step 2: Inject provisioning files.
	// The install step may have already injected (via stopVMAndInject) in
	// headless mode. Skip if injection already succeeded.
	if !didInjectSucceed() {
		fmt.Println()
		fmt.Println("=== Step 2/3: Provisioning VM ===")
		opts := InjectOptions{
			Config: ProvisionConfig{
				Username: cfg.user,
				Password: cfg.password,
				Admin:    true,
			},
			SkipSetupAssistant: true,
			AutoLogin:          true,
			InjectAgent:        sandboxAllowsAgentProvision(),
			InjectGuestTools:   !sandboxActive(),
		}
		if _, err := stageProvisioningFiles(opts); err != nil {
			return fmt.Errorf("stage provisioning: %w", err)
		}
		// Stage inject directives from vzscript recipes before applying.
		if cfg.vzscripts != "" {
			if err := stageVZScriptInjects(splitRecipes(cfg.vzscripts)); err != nil {
				return fmt.Errorf("stage vzscript injects: %w", err)
			}
		}
		if err := applyProvisioningFiles(); err != nil {
			return fmt.Errorf("apply provisioning: %w", err)
		}
	} else {
		fmt.Println()
		fmt.Println("=== Step 2/3: Provisioning (already done) ===")
	}

	// Step 3: Boot VM and optionally run vzscripts.
	recipes := splitRecipes(cfg.vzscripts)
	if len(recipes) > 0 {
		savePostInstallRecipes(vmDir, cfg.vzscripts)
		fmt.Println()
		fmt.Printf("=== Step 3/3: Boot + vzscripts (%s) ===\n", cfg.vzscripts)
		return runUpWithVZScripts(recipes, cfg.noShutdown, cfg.verbose)
	}

	fmt.Println()
	fmt.Println("=== Step 3/3: Booting VM ===")
	return runMacOSVM()
}

// runUpWithVZScripts boots the VM in a goroutine, runs the given vzscript
// recipes, and either shuts down the VM or leaves it running based on
// noShutdown. If the VM exits unexpectedly during script execution, the
// error is returned immediately.
func runUpWithVZScripts(recipes []string, noShutdown, verboseMode bool) error {
	// Validate all recipes before booting.
	for _, name := range recipes {
		if _, err := loadVZScriptData(name); err != nil {
			return fmt.Errorf("recipe %q: %w", name, err)
		}
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

		// Run each recipe in order.
		for _, name := range recipes {
			fmt.Printf("\n=== Running vzscript: %s ===\n", name)
			data, err := loadVZScriptData(name)
			if err != nil {
				scriptsDone <- err
				return
			}
			ar := txtar.Parse(data)
			meta := parseScriptMeta(ar.Comment)
			if len(meta.mounts) > 0 {
				if err := applyMountDirectives(meta.mounts, cfg.socketPath, cfg.verbose); err != nil {
					scriptsDone <- fmt.Errorf("recipe %s: mount: %w", name, err)
					return
				}
			}
			rcfg := cfgForRecipe(cfg, meta)
			if err := runVZScript(data, name, rcfg); err != nil {
				scriptsDone <- fmt.Errorf("%s: %w", name, err)
				return
			}
			fmt.Printf("=== Done: %s ===\n", name)
		}

		if noShutdown {
			fmt.Println("\nAll vzscripts complete. VM is still running.")
			fmt.Println("Use 'vz-macos ctl stop' to shut it down.")
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
		if err != nil && vmErr == nil {
			return err
		}
	default:
	}

	return vmErr
}

// runLinuxUpPipeline executes the install -> run pipeline for Linux VMs.
// Cloud-init handles user provisioning during install, so no inject step is needed.
func runLinuxUpPipeline(cfg upConfig) error {
	// Step 1: Install Linux VM (cloud-init provisions the user).
	fmt.Println("=== Step 1/2: Installing Linux VM ===")
	if err := installLinuxVM(); err != nil {
		return fmt.Errorf("install: %w", err)
	}

	// Step 2: Boot VM and optionally run vzscripts.
	recipes := splitRecipes(cfg.vzscripts)
	if len(recipes) > 0 {
		savePostInstallRecipes(vmDir, cfg.vzscripts)
		fmt.Println()
		fmt.Printf("=== Step 2/2: Boot + vzscripts (%s) ===\n", cfg.vzscripts)
		return runLinuxUpWithVZScripts(recipes, cfg.noShutdown, cfg.verbose)
	}

	fmt.Println()
	fmt.Println("=== Step 2/2: Booting Linux VM ===")
	return runLinuxVM()
}

// runLinuxUpWithVZScripts boots a Linux VM and runs vzscript recipes.
func runLinuxUpWithVZScripts(recipes []string, noShutdown, verboseMode bool) error {
	for _, name := range recipes {
		if _, err := loadVZScriptData(name); err != nil {
			return fmt.Errorf("recipe %q: %w", name, err)
		}
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

		for _, name := range recipes {
			fmt.Printf("\n=== Running vzscript: %s ===\n", name)
			data, err := loadVZScriptData(name)
			if err != nil {
				scriptsDone <- err
				return
			}
			ar := txtar.Parse(data)
			meta := parseScriptMeta(ar.Comment)
			if len(meta.mounts) > 0 {
				if err := applyMountDirectives(meta.mounts, cfg.socketPath, cfg.verbose); err != nil {
					scriptsDone <- fmt.Errorf("recipe %s: mount: %w", name, err)
					return
				}
			}
			rcfg := cfgForRecipe(cfg, meta)
			if err := runVZScript(data, name, rcfg); err != nil {
				scriptsDone <- fmt.Errorf("%s: %w", name, err)
				return
			}
			fmt.Printf("=== Done: %s ===\n", name)
		}

		if noShutdown {
			fmt.Println("\nAll vzscripts complete. VM is still running.")
			fmt.Println("Use 'vz-macos ctl stop' to shut it down.")
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

// stageVZScriptInjects loads the named vzscript recipes and their transitive
// dependencies, extracts any # inject: directives from their metadata, and
// stages the referenced txtar files into the existing provisioning staging
// directory. This runs between stageProvisioningFiles and applyProvisioningFiles
// so the inject files are included in the same disk-write pass.
func stageVZScriptInjects(recipes []string) error {
	stagingDir := provisionStagingDir()
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
