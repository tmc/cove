// This example demonstrates running macOS and Linux virtual machines using the
// generated purego bindings for Apple's Virtualization framework.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"text/tabwriter"

	"github.com/tmc/apple/x/vzkit"
)

var (
	fetchLatest bool
	runVM       bool // Deprecated: kept for compatibility, now handled by commands
	installVM   bool // Deprecated: kept for compatibility, now handled by commands
	guiMode     bool
	useCurl     bool
	linuxMode   bool
	cpuCount    uint
	memoryGB    uint64
	diskPath    string
	diskSizeGB  uint64
	ipswPath    string
	vmDir       string
	// Linux-specific flags
	kernelPath string
	initrdPath string
	cmdLine    string
	isoPath    string
	// Verbose flag
	verbose bool
	// Serial console output destination
	serialOutput string
	// Boot into recovery mode
	recoveryMode bool
	// Boot arguments (saved for use inside guest)
	bootArgs string
	// UTM bundle path
	utmBundlePath string
	// List VMs flag
	listVMs bool
	// Shared directory path for VirtioFS (deprecated, use -v)
	shareDir string
	// Volume mounts (docker-style -v flag)
	volumes volumeSlice
	// Provisioning flags
	provisionUser     string
	provisionPassword string
	provisionAdmin    bool
	provisionStrategy string
	// VM selection flag
	vmName string
	// Clone options
	cloneLinked bool
	// Network mode (nat, bridged:<iface>, vmnet, none)
	networkMode string
	// USB storage devices
	usbDevices USBStorageSlice
	// Display configurations
	displays DisplaySlice
	// Rosetta for Linux VMs
	enableRosetta bool
	// Clipboard sharing (SPICE agent)
	enableClipboard bool
	// vzscripts to run after install (comma-separated recipe names)
	installVZScripts string
	// Headless mode (disables GUI)
	headlessMode bool
	// Unattended install flags
	unattended       bool
	bootCommandsFile string
	debugOCR         bool
	// Auto-mount tagged volumes via agent
	autoMountVolumes bool
	// Force install over existing VM
	forceInstall bool
	// Skip restore from saved suspend state and cold boot instead.
	skipResume bool
	// GUI launch order experiment mode.
	launchOrder string
	// Runtime device profile for macOS VMs.
	runtimeProfile string
	// Stream Apple unified logs relevant to virtualization while running.
	appleLog bool
	// Custom unified log predicate for -apple-log.
	appleLogPredicate string
	// Allow destructive identity reset recovery when VM metadata is missing.
	recoverIdentity bool
	// Prefer GUI password dialogs over terminal prompts when available.
	preferPasswordDialog bool
)

func init() {
	flag.Usage = usage
	flag.BoolVar(&fetchLatest, "fetch-latest", false, "fetch latest supported restore image info")
	flag.BoolVar(&runVM, "run", false, "run a VM (macOS by default, use -linux for Linux)")
	flag.BoolVar(&installVM, "install", false, "install macOS from IPSW (uses -ipsw or fetches latest)")
	flag.BoolVar(&guiMode, "gui", true, "show VM display in a window (default: true)")
	flag.BoolVar(&headlessMode, "headless", false, "run without GUI window")
	flag.BoolVar(&useCurl, "curl", false, "use curl for IPSW download (resumable, saves to restore.ipsw)")
	flag.BoolVar(&linuxMode, "linux", false, "run a Linux VM instead of macOS")
	flag.BoolVar(&verbose, "verbose", false, "verbose output (includes run loop debugging)")
	flag.UintVar(&cpuCount, "cpu", 2, "number of CPUs")
	flag.Uint64Var(&memoryGB, "memory", 4, "memory in GB")
	flag.StringVar(&diskPath, "disk", "", "path to disk image")
	flag.Uint64Var(&diskSizeGB, "disk-size", 64, "disk size in GB for new disk images")
	flag.StringVar(&ipswPath, "ipsw", "", "path to IPSW restore image")
	flag.StringVar(&vmDir, "vm-dir", "", "directory for VM files (default: ~/.vz/vms/)")
	// Linux-specific flags
	flag.StringVar(&kernelPath, "kernel", "", "path to Linux kernel (vmlinuz) for direct boot")
	flag.StringVar(&initrdPath, "initrd", "", "path to initial ramdisk (initrd) for direct boot")
	flag.StringVar(&cmdLine, "cmdline", "", "kernel command line arguments")
	flag.StringVar(&isoPath, "iso", "", "path to ISO image for EFI boot installation")
	flag.StringVar(&serialOutput, "serial", "stdout", "serial console output: 'stdout', 'none', or file path")
	flag.BoolVar(&recoveryMode, "recovery", false, "boot into macOS recovery mode")
	flag.StringVar(&bootArgs, "boot-args", "", "boot arguments (e.g., 'serial=3 -v'); saved to vm-dir/boot-args.txt for use inside guest")
	flag.StringVar(&utmBundlePath, "utm", "", "path to UTM bundle (.utm) to run, or 'list' for menu")
	flag.BoolVar(&listVMs, "list", false, "list available VMs in vm-dir")
	flag.Var(&volumes, "v", "mount volume: /host/path[:tag][:ro] (can be repeated; tag defaults to auto-mount)")
	flag.Var(&volumes, "vol", "mount volume: /host/path[:tag][:ro] (can be repeated; tag defaults to auto-mount)")
	flag.StringVar(&shareDir, "share-dir", "", "(deprecated: use -vol) host directory to share with guest")
	flag.StringVar(&provisionUser, "provision-user", "", "username for auto-provisioned user (enables provisioning)")
	flag.StringVar(&provisionPassword, "provision-password", "", "password for auto-provisioned user")
	flag.BoolVar(&provisionAdmin, "provision-admin", true, "make auto-provisioned user an admin")
	flag.StringVar(&provisionStrategy, "provision-strategy", "inject",
		"provisioning strategy: inject (disk injection, needs sudo), gui (keyboard automation), auto (try inject, fall back to gui)")
	flag.StringVar(&installVZScripts, "vzscripts", "", "comma-separated vzscript recipes to run after install (e.g. homebrew,openclaw)")
	// VM selection flag
	flag.StringVar(&vmName, "vm", "", "VM name to use (default: active VM or 'default')")
	// Clone options
	flag.BoolVar(&cloneLinked, "linked", false, "create linked clone using APFS copy-on-write")
	// Network mode
	flag.StringVar(&networkMode, "network", "nat", "network mode: nat, bridged:<iface>, vmnet, none")
	// USB storage
	flag.Var(&usbDevices, "usb", "USB storage device: /path/to/disk.img[:ro] (can be repeated)")
	// Display configuration
	flag.Var(&displays, "display", "display config: WIDTHxHEIGHT[@PPI] or preset (4k, 1080p, 720p)")
	// Rosetta for Linux
	flag.BoolVar(&enableRosetta, "rosetta", false, "enable Rosetta for x86-64 binary translation in Linux VMs")
	// Clipboard sharing
	flag.BoolVar(&enableClipboard, "clipboard", true, "enable host↔guest clipboard sharing via SPICE agent (requires spice-vdagent in guest; macOS 15+ for macOS guests)")
	flag.BoolVar(&skipResume, "no-resume", false, "discard saved suspend state and perform a cold boot")
	flag.BoolVar(&skipResume, "cold-boot", false, "alias for -no-resume")
	flag.StringVar(&launchOrder, "launch-order", "window-first", "GUI launch order: window-first or start-first")
	flag.StringVar(&runtimeProfile, "runtime-profile", "full", "macOS runtime profile: full or minimal")
	flag.BoolVar(&appleLog, "apple-log", false, "stream Apple unified logs relevant to virtualization while running")
	flag.StringVar(&appleLogPredicate, "apple-log-predicate", "", "custom predicate for -apple-log (NSPredicate syntax)")
	flag.BoolVar(&recoverIdentity, "recover-identity", false, "if VM identity metadata is missing, back it up and reset identity files to attempt recovery")
	// Unattended install
	flag.BoolVar(&unattended, "unattended", false, "fully unattended install + setup (inject provisioning, OCR fallback)")
	flag.StringVar(&bootCommandsFile, "boot-commands", "", "path to boot commands file for custom setup automation")
	flag.BoolVar(&debugOCR, "debug-ocr", false, "save OCR debug screenshots with text bounding boxes")
	// Auto-mount volumes
	flag.BoolVar(&autoMountVolumes, "auto-mount-volumes", true, "auto-mount tagged volumes in guest via agent")
	// Force install (skip existing VM check)
	flag.BoolVar(&forceInstall, "force", false, "force install even if VM disk already exists (DESTROYS existing data)")
}

func main() {
	flag.Parse()

	// Validate mutually exclusive flags.
	if headlessMode && guiMode {
		// Both were explicitly set; check if -gui was passed explicitly.
		guiExplicit := false
		flag.Visit(func(f *flag.Flag) {
			if f.Name == "gui" {
				guiExplicit = true
			}
		})
		if guiExplicit {
			fmt.Fprintf(os.Stderr, "error: -gui and -headless are mutually exclusive\n")
			os.Exit(1)
		}
	}
	// --headless overrides --gui
	if headlessMode {
		guiMode = false
	}

	// Set up macgo bundling (entitlements, signing, app icon).
	// Must be before LockOSThread. May relaunch and not return.
	initMacgo()

	// Note: NSSetUncaughtExceptionHandler disabled — purego cannot marshal
	// struct types through callbacks (NSException is a Go struct wrapper).
	// ObjC exceptions will still surface as Go panics via the runtime.
	// foundation.NSSetUncaughtExceptionHandler(func(e foundation.NSException) {
	// 	panic("Exiting due to uncaught exception.")
	// })
	// Enable verbose logging if -v flag is set
	SetVerbose(verbose)

	// Resolve VM directory using registry (handles migration and VM selection)
	var err error
	vmDir, err = EnsureVMDir(vmName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Load saved VM config and apply defaults for flags not explicitly set.
	applyVMConfig(vmDir)

	// Prompt for password securely if -provision-user is set without -provision-password.
	preferPasswordDialog = guiMode && !headlessMode
	if provisionUser != "" && provisionPassword == "" {
		pw, err := readPassword(fmt.Sprintf("Password for %s: ", provisionUser))
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: could not read password: %v\n  use -provision-password <pw> to provide the password non-interactively\n", err)
			os.Exit(1)
		}
		provisionPassword = string(pw)
	}

	if err := validateLaunchOptions(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Legacy flag handling compatibility
	if fetchLatest {
		if _, err := fetchLatestRestoreImageObject(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if listVMs {
		handleList()
		return
	}
	if installVM {
		fmt.Fprintf(os.Stderr, "warning: -install flag is deprecated, use 'vz-macos install' command instead\n")
		var err error
		if linuxMode {
			err = handleLinuxInstall()
		} else {
			err = installMacOSLikeVZ(context.Background())
		}
		if errors.Is(err, errRestartVM) {
			if err := runMacOSVM(); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			return
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if utmBundlePath != "" {
		handleUTM()
		return
	}
	if runVM {
		fmt.Fprintf(os.Stderr, "warning: -run flag is deprecated, use 'vz-macos run' command instead\n")
		handleRun()
		return
	}
	if flag.NArg() > 0 {
		cmd := flag.Arg(0)
		args := flag.Args()[1:]

		// Commands that have their own flag parsing (don't re-parse with main flags)
		switch cmd {
		case "version":
			fmt.Println(versionInfo())
			return
		case "sip":
			if err := handleSIPCommand(args); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			return
		case "ctl":
			if err := ctlCommand(args); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			return
		case "inject":
			// inject is now an alias for provision
			if err := handleProvision(args); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			return
		case "inject-agent":
			// Shorthand for "inject -agent" (no user provisioning)
			if err := injectAgentOnly(); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			return
		case "verify", "doctor":
			if err := handleVerify(args); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			return
		case "disk-detach":
			diskFile := filepath.Join(vmDir, "disk.img")
			if err := ensureDiskDetached(diskFile); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			return
		case "up":
			if err := handleUp(args); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			return
		case "vzscript":
			if err := vzscriptCommand(args); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			return
		case "uiscript":
			fmt.Fprintf(os.Stderr, "The 'uiscript' command has been merged into 'vzscript'.\nUse 'vz-macos vzscript' instead.\n")
			os.Exit(1)
			return
		}

		// Re-parse remaining args so flags after the subcommand work
		// (e.g., "vz-macos run -gui" parses -gui here).
		flag.CommandLine.Parse(args)

		// Re-apply verbose after subcommand flag re-parse.
		SetVerbose(verbose)

		// --headless overrides --gui after subcommand re-parse
		if headlessMode {
			guiMode = false
		}
		if err := validateLaunchOptions(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		// Re-resolve vmDir if -vm flag was provided after subcommand
		if vmName != "" {
			vmDir, err = EnsureVMDir(vmName)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
		}

		switch cmd {
		case "install":
			installVM = true
			var err error
			if linuxMode {
				err = handleLinuxInstall()
			} else {
				err = installMacOSLikeVZ(context.Background())
			}
			if errors.Is(err, errRestartVM) {
				if err := runMacOSVM(); err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					os.Exit(1)
				}
			} else if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			if installVZScripts != "" {
				if err := runPostInstallVZScripts(installVZScripts); err != nil {
					fmt.Fprintf(os.Stderr, "error: running vzscripts: %v\n", err)
					os.Exit(1)
				}
			}
			return
		case "run":
			handleRun()
			return
		case "list":
			handleList()
			return
		case "provision":
			fmt.Fprintf(os.Stderr, "warning: 'provision' command is deprecated, use 'inject' instead\n")
			if err := handleProvision(args); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			return
		case "clean":
			if err := cleanVM(); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			return
		case "clone":
			handleClone(args)
			return
		case "template":
			handleTemplate(args)
			return
		case "vm":
			handleVMCommand(args)
			return
		case "snapshot":
			handleSnapshotCommand(args)
			return
		case "disk-snapshot":
			handleDiskSnapshotCommand(args)
			return
		case "network":
			handleNetworkCommand(args)
			return
		case "rosetta":
			if err := handleRosettaCommand(args); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			return
		default:
			fmt.Fprintf(os.Stderr, "Unknown command: %s\nRun 'vz-macos -help' for usage.\n", cmd)
			os.Exit(1)
		}
	}

	// Default: smart routing based on number of VMs
	handleDefaultAction()
}

// handleDefaultAction routes based on the number of existing VMs:
//   - 0 VMs: start guided install
//   - 1 VM: show the native selector in GUI mode, otherwise run it directly
//   - 2+ VMs: show native VM selector window
func handleDefaultAction() {
	vms, err := ListVMs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: listing VMs: %v\n", err)
		os.Exit(1)
	}
	if len(vms) == 0 {
		if info, err := GetVMInfo(vmDir); err == nil {
			vms = append(vms, *info)
		}
	}
	if guiMode && len(vms) > 0 {
		showVMSelectorWindow(vms)
		return
	}

	switch len(vms) {
	case 0:
		guiMode = true
		err := installMacOSLikeVZ(context.Background())
		if errors.Is(err, errRestartVM) {
			err = runMacOSVM()
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case 1:
		vmDir = vms[0].Path
		vmName = vms[0].Name
		guiMode = true
		if vms[0].OSType == "Linux" {
			linuxMode = true
		}
		handleRun()
	default:
		showVMSelectorWindow(vms)
	}
}

func handleUTM() {
	var err error
	if utmBundlePath == "list" || utmBundlePath == "." {
		// Show launcher to pick from available VMs
		if guiMode {
			// GUI mode: use file picker dialog
			err = runUTMLauncherGUI()
		} else {
			// CLI mode: show text-based menu
			err = runUTMLauncher()
		}
	} else {
		// Run specific bundle
		err = runUTMBundle(utmBundlePath)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func handleRun() {
	var err error
	if linuxMode {
		err = runLinuxVM()
	} else {
		err = runMacOSVM()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `vz-macos - Apple Virtualization Framework Example

Usage:
  vz-macos [flags] [command]

Commands:
  up          Install + provision + boot in one command (vz-macos up -user me)
  install     Install OS (macOS from IPSW, -linux for Ubuntu)
  run         Run a VM (macOS by default, -linux for Linux)
  list        List available VMs and templates
  provision   (deprecated: use 'inject' instead)
  inject      Inject provisioning files directly into VM disk (self-contained)
  verify      Verify provisioning files in VM disk (alias: doctor)
  clean       Remove VM files (disk, aux, hw.model, machine.id)
  clone       Clone a VM (vz-macos clone [source] <target> [--linked])
  template    Manage VM templates (save/list/create)
  vm          VM management (set/delete/rename/export/import)
  sip         SIP management (enable/disable/status + recovery automation)
  ctl         Control running VM via socket (screenshot, status, pause, etc.)
  snapshot    Manage VM snapshots (list/save/restore/delete)
  network     Network configuration (list interfaces, help)
  rosetta     Rosetta 2 for Linux VMs (status/install/setup)
  disk-snapshot Manage disk-level snapshots (APFS clonefile, copy-on-write)
  disk-detach Detach VM disk if stuck from a previous inject/verify
  inject-agent Inject only vz-agent daemon (no user provisioning)
  vzscript    Run guest-agent and UI automation scripts (rsc.io/script + txtar)
  version     Print version information

Auto-Provisioning (Recommended - inject command):
  Inject user provisioning directly into VM disk (no VirtioFS needed):

  vz-macos install -ipsw restore.ipsw
  vz-macos inject -user testuser -skip-setup-assistant  # prompts for password
  vz-macos run

  This creates a self-contained LaunchDaemon that:
  - Runs on first boot to create the user account
  - Skips Setup Assistant entirely (with -skip-setup-assistant)
  - Self-cleans after execution

Auto-Provisioning (Alternative - GUI automation):
  Use -provision-user with -gui to automate user creation (prompts for password):

  vz-macos run -gui -provision-user testuser

  This will:
  1. Start the VM with GUI window
  2. Detect when Setup Assistant appears
  3. Navigate through setup using keyboard automation
  4. Create the specified user account
  5. Proceed to desktop

Provisioning Strategy (-provision-strategy):
  inject (default)  Stop VM after install, mount disk, write LaunchDaemon.
                    On first boot, launchd creates user. Needs sudo.
  gui               Skip disk injection. On first boot, navigate Setup
                    Assistant via keyboard automation. No sudo needed.
  auto              Try inject first. If it fails, fall back to gui.

Linux VM (Ubuntu):
  Install and run Ubuntu Server ARM64 using cloud-init autoinstall:

  vz-macos install -linux                                    # Auto-downloads Ubuntu
  vz-macos install -linux -iso /path/to/ubuntu.iso           # Use local ISO
  vz-macos install -linux -provision-user me -provision-password pw  # With user
  vz-macos run -linux                                        # Run installed VM
  vz-macos run -linux -gui                                   # Run with display

Volume Mounting (-vol flag):
  Docker-style volume mounts. Format: /host/path[:tag][:ro|rw]

  Examples:
    -vol /path/to/dir                   Mount at /Volumes/My Shared Files (rw)
    -vol /path/to/dir:ro                Mount at /Volumes/My Shared Files (read-only)
    -vol /path/to/dir:MyData            Mount at /Volumes/MyData (rw)
    -vol /path/to/dir:MyData:ro         Mount at /Volumes/MyData (read-only)
    -vol ~/code:Code -vol ~/data:Data   Multiple volumes

Flags:
`)
	flag.PrintDefaults()
}

func validateLaunchOptions() error {
	switch provisionStrategy {
	case "inject", "gui", "auto":
	default:
		return fmt.Errorf("invalid -provision-strategy %q (must be inject, gui, or auto)", provisionStrategy)
	}

	switch launchOrder {
	case "window-first", "start-first":
	default:
		return fmt.Errorf("invalid -launch-order %q (must be window-first or start-first)", launchOrder)
	}

	switch runtimeProfile {
	case "full", "minimal":
	default:
		return fmt.Errorf("invalid -runtime-profile %q (must be full or minimal)", runtimeProfile)
	}
	return nil
}

// handleList shows all VMs and templates.
func handleList() {
	// List VMs
	vms, err := ListVMs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: listing VMs: %v\n", err)
		os.Exit(1)
	}

	activeVM := GetActiveVM()

	if len(vms) == 0 {
		fmt.Println("No VMs found. Run 'vz-macos install' to create one.")
	} else {
		fmt.Println("VMs:")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "  NAME\tSIZE\tCREATED\tACTIVE")
		for _, vm := range vms {
			active := ""
			if vm.Name == activeVM {
				active = "*"
			}
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n",
				vm.Name,
				FormatSize(vm.DiskSize),
				vm.Created.Format("2006-01-02"),
				active)
		}
		w.Flush()
	}

	// List templates
	templates, err := ListTemplates()
	if err == nil && len(templates) > 0 {
		fmt.Println()
		fmt.Println("Templates:")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "  NAME\tSIZE\tCREATED")
		for _, t := range templates {
			fmt.Fprintf(w, "  %s\t%s\t%s\n",
				t.Name,
				FormatSize(t.DiskSize),
				t.Created.Format("2006-01-02"))
		}
		w.Flush()
	}
}

// handleClone handles the clone subcommand.
func handleClone(args []string) {
	// Parse: clone [source] <target> [--linked]
	// If only one arg, source is default/active VM
	var source, target string

	nonFlagArgs := []string{}
	for _, arg := range args {
		if arg == "--linked" || arg == "-linked" {
			cloneLinked = true
		} else if arg[0] != '-' {
			nonFlagArgs = append(nonFlagArgs, arg)
		}
	}

	switch len(nonFlagArgs) {
	case 0:
		fmt.Fprintln(os.Stderr, "Usage: vz-macos clone [source] <target> [--linked]")
		os.Exit(1)
	case 1:
		source = GetActiveVM()
		target = nonFlagArgs[0]
	default:
		source = nonFlagArgs[0]
		target = nonFlagArgs[1]
	}

	err := CloneVM(CloneOptions{
		Source:        source,
		Target:        target,
		Linked:        cloneLinked,
		CopyMachineID: false,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// handleTemplate handles the template subcommand.
func handleTemplate(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, `Usage: vz-macos template <command>

Commands:
  save <name>                Save current VM as template (compressed)
  save-fast <name>           Save as fast template (clonefile, instant but larger)
  list                       List available templates
  create <template> <name>   Create VM from template
  delete <name>              Delete a template

Fast templates use APFS clonefile for instant copy-on-write creation.
Compressed templates take longer to save but use less disk space.`)
		os.Exit(1)
	}

	subcmd := args[0]
	subargs := args[1:]

	switch subcmd {
	case "save":
		if len(subargs) < 1 {
			fmt.Fprintln(os.Stderr, "Usage: vz-macos template save <name>")
			os.Exit(1)
		}
		source := GetActiveVM()
		if vmName != "" {
			source = vmName
		}
		if err := SaveTemplate(source, subargs[0]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "save-fast":
		if len(subargs) < 1 {
			fmt.Fprintln(os.Stderr, "Usage: vz-macos template save-fast <name>")
			os.Exit(1)
		}
		source := GetActiveVM()
		if vmName != "" {
			source = vmName
		}
		if err := SaveTemplateFast(source, subargs[0]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "list":
		templates, err := ListTemplates()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if len(templates) == 0 {
			fmt.Println("No templates found.")
			return
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tSIZE\tMODE\tCREATED")
		for _, t := range templates {
			mode := "compressed"
			if t.FastMode {
				mode = "fast"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
				t.Name,
				FormatSize(t.DiskSize),
				mode,
				t.Created.Format("2006-01-02"))
		}
		w.Flush()

	case "create":
		if len(subargs) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: vz-macos template create <template> <name>")
			os.Exit(1)
		}
		if err := CreateFromTemplate(subargs[0], subargs[1]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "delete":
		if len(subargs) < 1 {
			fmt.Fprintln(os.Stderr, "Usage: vz-macos template delete <name>")
			os.Exit(1)
		}
		if err := DeleteTemplate(subargs[0]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Template '%s' deleted.\n", subargs[0])

	default:
		fmt.Fprintf(os.Stderr, "Unknown template command: %s\n", subcmd)
		os.Exit(1)
	}
}

// handleVMCommand handles the vm subcommand.
func handleVMCommand(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, `Usage: vz-macos vm <command>

Commands:
  set <name>              Set active VM
  delete <name>           Delete a VM
  rename <old> <new>      Rename a VM
  export <name> <path>    Export VM to tarball
  import <path> <name>    Import VM from tarball
  shared-folder ...       Manage runtime shared folders`)
		os.Exit(1)
	}

	subcmd := args[0]
	subargs := args[1:]

	switch subcmd {
	case "set":
		if len(subargs) < 1 {
			fmt.Fprintln(os.Stderr, "Usage: vz-macos vm set <name>")
			os.Exit(1)
		}
		if err := SetActiveVM(subargs[0]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Active VM set to '%s'.\n", subargs[0])

	case "delete":
		if len(subargs) < 1 {
			fmt.Fprintln(os.Stderr, "Usage: vz-macos vm delete <name>")
			os.Exit(1)
		}
		if err := DeleteVM(subargs[0]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "rename":
		if len(subargs) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: vz-macos vm rename <old> <new>")
			os.Exit(1)
		}
		if err := RenameVM(subargs[0], subargs[1]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "export":
		if len(subargs) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: vz-macos vm export <name> <path>")
			os.Exit(1)
		}
		if err := ExportVM(subargs[0], subargs[1]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "import":
		if len(subargs) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: vz-macos vm import <path> <name>")
			os.Exit(1)
		}
		if err := ImportVM(subargs[0], subargs[1]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "shared-folder", "shared-folders":
		if err := handleVMSharedFolderCommand(subargs); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "Unknown vm command: %s\n", subcmd)
		os.Exit(1)
	}
}

// handleSnapshotCommand handles the snapshot subcommand
func handleSnapshotCommand(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, `Usage: vz-macos snapshot <command>

Commands:
  list                    List available snapshots
  save <name>             Save current VM state (VM must be running)
  restore <name>          Restore VM from snapshot (VM must be running)
  delete <name>           Delete a snapshot

Snapshots are saved to ~/.vz/vms/<vmname>/snapshots/

Examples:
  vz-macos snapshot list
  vz-macos snapshot save checkpoint1
  vz-macos snapshot restore checkpoint1
  vz-macos snapshot delete checkpoint1`)
		os.Exit(1)
	}

	mgr := vzkit.NewSnapshotManager(vmDir)
	subcmd := args[0]
	subargs := args[1:]

	switch subcmd {
	case "list":
		snapshots, err := mgr.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if len(snapshots) == 0 {
			fmt.Println("No snapshots found.")
			return
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tSIZE\tCREATED")
		for _, s := range snapshots {
			fmt.Fprintf(w, "%s\t%s\t%s\n",
				s.Name,
				FormatSize(s.Size),
				s.Created.Format("2006-01-02 15:04"))
		}
		w.Flush()

	case "delete":
		if len(subargs) < 1 {
			fmt.Fprintln(os.Stderr, "Usage: vz-macos snapshot delete <name>")
			os.Exit(1)
		}
		if err := mgr.Delete(subargs[0]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "save", "restore":
		if len(subargs) < 1 {
			fmt.Fprintf(os.Stderr, "Usage: vz-macos snapshot %s <name>\n", subcmd)
			os.Exit(1)
		}
		if err := snapshotViaControlSocket(subcmd, subargs[0]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "Unknown snapshot command: %s\n", subcmd)
		os.Exit(1)
	}
}

// handleNetworkCommand handles the network subcommand
func handleNetworkCommand(args []string) {
	if len(args) == 0 {
		fmt.Println(NetworkModeHelp())
		return
	}

	switch args[0] {
	case "list":
		printNetworkInterfaces()
	case "help":
		fmt.Println(NetworkModeHelp())
	default:
		fmt.Fprintf(os.Stderr, "Unknown network command: %s\n", args[0])
		fmt.Println("\nUsage: vz-macos network [list|help]")
		os.Exit(1)
	}
}
