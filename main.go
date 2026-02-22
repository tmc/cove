// This example demonstrates running macOS and Linux virtual machines using the
// generated purego bindings for Apple's Virtualization framework.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"text/tabwriter"

	"golang.org/x/term"
)

var (
	fetchLatest bool
	runVM       bool // Deprecated: kept for compatibility, now handled by commands
	installVM   bool // Deprecated: kept for compatibility, now handled by commands
	guiMode     bool
	useCurl     bool
	linuxMode   bool
	windowsMode bool // TODO: re-enable when Apple adds GOP linear framebuffer to VZEFIBootLoader
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
	// Attach recovery tools disk (FAT32 with csrutil scripts)
	recoveryDisk bool
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
	// User data disk (separate from system)
	enableUserData    bool
	userDataPath      string
	userDataSizeGB    uint64
	userDataReadOnly  bool
	userDataStrategy  string
	userDataEphemeral bool
	// Clipboard sharing (SPICE agent)
	enableClipboard bool
	// Scripts share (VirtioFS for guest agent)
	enableScripts    bool
	scriptsPath      string
	scriptsReadOnly  bool
	scriptsRunOnBoot bool
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
	flag.BoolVar(&windowsMode, "windows", false, "run a Windows ARM64 VM instead of macOS")
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
	flag.BoolVar(&recoveryDisk, "recovery-disk", false, "attach recovery FAT32 disk with SIP tools (auto-created)")
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
	// User data disk (separate from system)
	flag.BoolVar(&enableUserData, "userdata", false, "use separate disk for user data")
	flag.StringVar(&userDataPath, "userdata-path", "", "path to user data disk image")
	flag.Uint64Var(&userDataSizeGB, "userdata-size", 32, "size of user data disk in GB")
	flag.BoolVar(&userDataReadOnly, "userdata-ro", false, "mount user data disk as read-only")
	flag.StringVar(&userDataStrategy, "userdata-strategy", "volumes", "mount strategy: volumes, symlinks, direct")
	flag.BoolVar(&userDataEphemeral, "userdata-ephemeral", false, "discard user data changes after VM stops")
	// Clipboard sharing
	flag.BoolVar(&enableClipboard, "clipboard", true, "enable host↔guest clipboard sharing (SPICE agent)")
	// Scripts share (VirtioFS for guest agent)
	flag.BoolVar(&enableScripts, "scripts", false, "enable VirtioFS scripts share for guest agent")
	flag.StringVar(&scriptsPath, "scripts-path", "", "path to scripts directory (default: vmDir/scripts/)")
	flag.BoolVar(&scriptsReadOnly, "scripts-ro", false, "mount scripts share as read-only")
	flag.BoolVar(&scriptsRunOnBoot, "scripts-run", true, "run bootstrap script on boot (default: true)")
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
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Load saved VM config and apply defaults for flags not explicitly set.
	applyVMConfig(vmDir)

	// Prompt for password securely if -provision-user is set without -provision-password.
	// Use /dev/tty directly — macgo may have replaced os.Stdin with a pipe.
	if provisionUser != "" && provisionPassword == "" {
		if tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
			fmt.Fprintf(tty, "Password for %s: ", provisionUser)
			pw, err := term.ReadPassword(int(tty.Fd()))
			fmt.Fprintln(tty) // newline after hidden input
			tty.Close()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error reading password: %v\n", err)
				os.Exit(1)
			}
			provisionPassword = string(pw)
		}
	}

	switch provisionStrategy {
	case "inject", "gui", "auto":
	default:
		fmt.Fprintf(os.Stderr, "Error: invalid -provision-strategy %q (must be inject, gui, or auto)\n", provisionStrategy)
		os.Exit(1)
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Legacy flag handling compatibility
	if fetchLatest {
		if _, err := fetchLatestRestoreImageObject(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if listVMs {
		handleList()
		return
	}
	if installVM {
		var err error
		if windowsMode {
			err = windowsNotSupported()
		} else if linuxMode {
			err = handleLinuxInstall()
		} else {
			err = installMacOSLikeVZ(context.Background())
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if utmBundlePath != "" {
		handleUTM()
		return
	}
	if runVM {
		handleRun()
		return
	}
	if flag.NArg() > 0 {
		cmd := flag.Arg(0)
		args := flag.Args()[1:]

		// Commands that have their own flag parsing (don't re-parse with main flags)
		switch cmd {
		case "ctl":
			if err := ctlCommand(args); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "inject":
			// inject is now an alias for provision
			if err := handleProvision(args); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "inject-agent":
			// Shorthand for "inject -agent" (no user provisioning)
			if err := injectAgentOnly(); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "verify", "doctor":
			if err := handleVerify(args); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "disk-detach":
			diskFile := filepath.Join(vmDir, "disk.img")
			if err := ensureDiskDetached(diskFile); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "sip":
			if err := handleSIPCommand(args); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "fda":
			if err := handleFDACommand(args); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "vzscript":
			if err := vzscriptCommand(args); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		}

		// Re-parse remaining args so flags after the subcommand work
		// (e.g., "vz-macos run -gui" parses -gui here).
		flag.CommandLine.Parse(args)

		// --headless overrides --gui after subcommand re-parse
		if headlessMode {
			guiMode = false
		}

		// Re-resolve vmDir if -vm flag was provided after subcommand
		if vmName != "" {
			vmDir, err = EnsureVMDir(vmName)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
		}

		switch cmd {
		case "install":
			installVM = true
			var err error
			if windowsMode {
				err = windowsNotSupported()
			} else if linuxMode {
				err = handleLinuxInstall()
			} else {
				err = installMacOSLikeVZ(context.Background())
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			if installVZScripts != "" {
				if err := runPostInstallVZScripts(installVZScripts); err != nil {
					fmt.Fprintf(os.Stderr, "Error running vzscripts: %v\n", err)
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
		case "menubar":
			if err := RunMenubarApp(vmDir); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "provision":
			if err := setupProvisioning(); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "clean":
			if err := cleanVM(); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
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
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "userdata":
			if err := handleUserDataCommand(args); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			return
		case "setup":
			fmt.Fprintf(os.Stderr, "Error: setup command not yet implemented\n")
			os.Exit(1)
		case "recovery-disk":
			if err := sipCreateDisk(); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
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
//   - 1 VM: run it directly
//   - 2+ VMs: show native VM selector window
func handleDefaultAction() {
	vms, err := ListVMs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing VMs: %v\n", err)
		os.Exit(1)
	}

	switch len(vms) {
	case 0:
		guiMode = true
		if err := installMacOSLikeVZ(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
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
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// windowsNotSupported returns an error explaining why Windows is not available.
// Apple's Virtualization.framework lacks the GOP linear framebuffer that Windows
// Boot Manager requires. See windows.go for full technical details.
func windowsNotSupported() error {
	return fmt.Errorf("Windows ARM64 is not supported on Apple's Virtualization.framework.\n" +
		"The Windows Boot Manager requires a linear framebuffer (GOP) that Apple's\n" +
		"built-in EFI firmware does not provide. Use UTM (which uses QEMU) instead.\n" +
		"See windows.go for technical details and tracking information")
}

func handleRun() {
	var err error
	if windowsMode {
		err = windowsNotSupported()
	} else if linuxMode {
		err = runLinuxVM()
	} else {
		err = runMacOSVM()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `vz-macos - Apple Virtualization Framework Example

Usage:
  vz-macos [flags] [command]

Commands:
  install     Install OS (macOS from IPSW, -linux for Ubuntu)
  run         Run a VM (macOS by default, -linux for Linux)
  list        List available VMs and templates
  menubar     Run as a menubar app for VM control
  provision   Setup provisioning directory for auto-user-creation (deprecated)
  inject      Inject provisioning files directly into VM disk (self-contained)
  verify      Verify provisioning files in VM disk (check ownership, existence)
  clean       Remove VM files (disk, aux, hw.model, machine.id)
  clone       Clone a VM (vz-macos clone [source] <target> [--linked])
  template    Manage VM templates (save/list/create)
  vm          VM management (set/delete/rename/export/import)
  ctl         Control running VM via socket (screenshot, status, pause, etc.)
  snapshot    Manage VM snapshots (list/save/restore/delete)
  network     Network configuration (list interfaces, help)
  rosetta     Rosetta 2 for Linux VMs (status/install/setup)
  userdata    Separate user data disk (help/setup/workflow)
  setup       Software installation modules (list/enable/disable)
  disk-detach Detach VM disk if stuck from a previous inject/verify
  sip         SIP management (disable/enable/status)
  recovery-disk Create recovery tools disk (FAT32 with csrutil scripts)

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

// handleList shows all VMs and templates.
func handleList() {
	// List VMs
	vms, err := ListVMs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing VMs: %v\n", err)
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
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
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
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
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
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "list":
		templates, err := ListTemplates()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
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
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "delete":
		if len(subargs) < 1 {
			fmt.Fprintln(os.Stderr, "Usage: vz-macos template delete <name>")
			os.Exit(1)
		}
		if err := DeleteTemplate(subargs[0]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
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
  import <path> <name>    Import VM from tarball`)
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
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Active VM set to '%s'.\n", subargs[0])

	case "delete":
		if len(subargs) < 1 {
			fmt.Fprintln(os.Stderr, "Usage: vz-macos vm delete <name>")
			os.Exit(1)
		}
		if err := DeleteVM(subargs[0]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "rename":
		if len(subargs) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: vz-macos vm rename <old> <new>")
			os.Exit(1)
		}
		if err := RenameVM(subargs[0], subargs[1]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "export":
		if len(subargs) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: vz-macos vm export <name> <path>")
			os.Exit(1)
		}
		if err := ExportVM(subargs[0], subargs[1]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "import":
		if len(subargs) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: vz-macos vm import <path> <name>")
			os.Exit(1)
		}
		if err := ImportVM(subargs[0], subargs[1]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
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
  save <name>             Save current VM state (must connect to running VM)
  restore <name>          Restore VM from snapshot (must connect to running VM)
  delete <name>           Delete a snapshot

Snapshots are saved to ~/.vz/vms/<vmname>/snapshots/

For save/restore operations, the VM must be running and you must use
the control socket. Example:

  # Save snapshot via control socket
  echo '{"type":"snapshot","data":{"action":"save","name":"checkpoint1"}}' | \
    nc -U ~/.vz/vms/default/control.sock

  # Restore snapshot via control socket
  echo '{"type":"snapshot","data":{"action":"restore","name":"checkpoint1"}}' | \
    nc -U ~/.vz/vms/default/control.sock`)
		os.Exit(1)
	}

	mgr := NewSnapshotManager(vmDir)
	subcmd := args[0]
	subargs := args[1:]

	switch subcmd {
	case "list":
		snapshots, err := mgr.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
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
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

	case "save", "restore":
		fmt.Fprintln(os.Stderr, "Save/restore operations require a running VM.")
		fmt.Fprintln(os.Stderr, "Use the control socket to save/restore:")
		fmt.Fprintf(os.Stderr, "\n  echo '{\"type\":\"snapshot\",\"data\":{\"action\":\"%s\",\"name\":\"%s\"}}' | \\\n    nc -U %s\n\n",
			subcmd, "your-snapshot-name", GetControlSocketPath())
		os.Exit(1)

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

// getUserDataConfig builds UserDataConfig from CLI flags
func getUserDataConfig() UserDataConfig {
	strategy, err := ParseMountStrategy(userDataStrategy)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v, using 'volumes'\n", err)
		strategy = MountStrategyVolumes
	}

	config := UserDataConfig{
		Enabled:       enableUserData,
		SizeGB:        userDataSizeGB,
		ReadOnly:      userDataReadOnly,
		MountStrategy: strategy,
		Ephemeral:     userDataEphemeral,
	}
	if userDataPath != "" {
		config.Path = userDataPath
	} else if enableUserData {
		config.Path = DefaultUserDataPath(vmDir)
	}
	return config
}
