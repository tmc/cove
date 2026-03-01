// up.go — Single command to install, provision, and optionally run vzscripts.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

// handleUp implements the "up" subcommand: install → inject → run → vzscripts.
func handleUp(args []string) error {
	fs := flag.NewFlagSet("up", flag.ExitOnError)
	user := fs.String("user", "", "Username for the provisioned user (required)")
	password := fs.String("password", "", "Password for the provisioned user (prompts if empty)")
	vzscripts := fs.String("vzscripts", "", "Comma-separated vzscript recipes to run after boot (e.g. homebrew,openclaw)")
	ipsw := fs.String("ipsw", "", "Path to IPSW restore image (downloads latest if empty)")
	force := fs.Bool("force", false, "Force install even if VM disk already exists")
	gui := fs.Bool("gui", true, "Show VM display in a window")
	headless := fs.Bool("headless", false, "Run without GUI window")
	cpu := fs.Uint("cpu", 2, "Number of CPUs")
	mem := fs.Uint64("memory", 4, "Memory in GB")
	diskSize := fs.Uint64("disk-size", 64, "Disk size in GB")
	noShutdown := fs.Bool("no-shutdown", false, "Leave VM running after vzscripts complete")
	verboseFlag := fs.Bool("v", false, "Verbose output")
	vm := fs.String("vm", "", "VM name (default: active VM or 'default')")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: vz-macos up [options]

Install, provision, and boot a macOS VM in one command.

Combines: install → inject (user, auto-login, skip-setup, agent) → run [→ vzscripts]

Options:
`)
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, `
Examples:
  # Basic: install + provision + boot to desktop
  vz-macos up -user me

  # With vzscripts: install + provision + boot + run recipes
  vz-macos up -user me -vzscripts homebrew,openclaw

  # Using existing IPSW
  vz-macos up -user me -ipsw ~/restore.ipsw

  # Headless with more resources
  vz-macos up -user me -headless -cpu 4 -memory 8 -vzscripts developer-tools
`)
	}

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *user == "" {
		return fmt.Errorf("missing required flag: -user")
	}

	// Apply flags to globals used by the install/run/inject machinery.
	if *vm != "" {
		vmName = *vm
		var err error
		vmDir, err = EnsureVMDir(vmName)
		if err != nil {
			return err
		}
	}
	if *ipsw != "" {
		ipswPath = *ipsw
	}
	forceInstall = *force
	cpuCount = *cpu
	memoryGB = *mem
	diskSizeGB = *diskSize
	verbose = *verboseFlag
	SetVerbose(verbose)

	if *headless {
		guiMode = false
	} else {
		guiMode = *gui
	}

	// Prompt for password if not provided.
	if *password == "" {
		pw, err := readPassword(fmt.Sprintf("Password for %s: ", *user))
		if err != nil {
			return fmt.Errorf("read password: %w", err)
		}
		*password = string(pw)
		if *password == "" {
			return fmt.Errorf("password required")
		}
	}
	provisionUser = *user
	provisionPassword = *password
	provisionStrategy = "inject"

	// Step 1: Install macOS.
	fmt.Println("=== Step 1/3: Installing macOS ===")
	installVM = true
	if err := installMacOSLikeVZ(context.Background()); err != nil {
		return fmt.Errorf("install: %w", err)
	}

	// Step 2: Inject provisioning files.
	// The install step may have already injected (via stopVMAndInject) but
	// only if running headless. If injection succeeded, skip this step.
	if !didInjectSucceed() {
		fmt.Println()
		fmt.Println("=== Step 2/3: Provisioning VM ===")
		opts := InjectOptions{
			Config: ProvisionConfig{
				Username: *user,
				Password: *password,
				Admin:    true,
			},
			SkipSetupAssistant: true,
			AutoLogin:          true,
			InjectAgent:        true,
			InjectGuestTools:   true,
		}
		if _, err := stageProvisioningFiles(opts); err != nil {
			return fmt.Errorf("stage provisioning: %w", err)
		}
		if err := applyProvisioningFiles(); err != nil {
			return fmt.Errorf("apply provisioning: %w", err)
		}
	} else {
		fmt.Println()
		fmt.Println("=== Step 2/3: Provisioning (already done) ===")
	}

	// Step 3: Boot VM and optionally run vzscripts.
	if *vzscripts != "" {
		fmt.Println()
		fmt.Printf("=== Step 3/3: Boot + vzscripts (%s) ===\n", *vzscripts)
		if *noShutdown {
			return runUpWithVZScriptsNoShutdown(*vzscripts)
		}
		return runPostInstallVZScripts(*vzscripts)
	}

	fmt.Println()
	fmt.Println("=== Step 3/3: Booting VM ===")
	return runMacOSVM()
}

// runUpWithVZScriptsNoShutdown boots the VM, runs vzscripts, then leaves
// the VM running instead of shutting it down.
func runUpWithVZScriptsNoShutdown(recipes string) error {
	// Boot the VM in a goroutine.
	vmErr := make(chan error, 1)
	go func() {
		vmErr <- runMacOSVM()
	}()

	sock := GetControlSocketPath()
	cfg := vzscriptConfig{
		socketPath:  sock,
		execTimeout: 30 * time.Minute,
		verbose:     verbose,
	}

	// Wait for the agent.
	fmt.Println("Waiting for VM to boot and guest agent...")
	waitScript := []byte("guest-wait 15m\n")
	if err := runVZScript(waitScript, "wait-for-agent", cfg); err != nil {
		return fmt.Errorf("waiting for agent: %w", err)
	}

	// Run each recipe.
	for _, name := range splitRecipes(recipes) {
		fmt.Printf("\n=== Running vzscript: %s ===\n", name)
		data, err := loadVZScriptData(name)
		if err != nil {
			return err
		}
		if err := runVZScript(data, name, cfg); err != nil {
			return fmt.Errorf("vzscript %s: %w", name, err)
		}
		fmt.Printf("=== Done: %s ===\n", name)
	}

	fmt.Println("\nAll vzscripts complete. VM is still running.")
	fmt.Println("Use 'vz-macos ctl stop' to shut it down.")

	// Wait for the VM to exit (user will close it manually).
	if err := <-vmErr; err != nil {
		return err
	}
	return nil
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
