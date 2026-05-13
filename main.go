// This example demonstrates running macOS and Linux virtual machines using the
// generated purego bindings for Apple's Virtualization framework.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	displayx "github.com/tmc/apple/x/vzkit/display"
	snapshotx "github.com/tmc/apple/x/vzkit/snapshot"
	"github.com/tmc/vz-macos/internal/action"
	"github.com/tmc/vz-macos/internal/bytefmt"
	"github.com/tmc/vz-macos/internal/vmconfig"
	"golang.org/x/term"
)

func init() {
	runtime.LockOSThread()
}

var (
	fetchLatest bool
	runVM       bool // Deprecated: kept for compatibility, now handled by commands
	installVM   bool // Deprecated: kept for compatibility, now handled by commands
	guiMode     bool

	linuxMode             bool
	windowsMode           bool
	linuxDesktop          bool
	nixosMode             bool
	linuxDistro           string
	linuxNested           bool
	linuxNVMe             bool
	linuxShell            bool
	linuxDesktopInstaller string
	cpuCount              uint
	cpuExplicit           bool
	memoryGB              uint64
	diskPath              string
	diskSizeGB            uint64
	rawDisk               bool
	ipswPath              string
	vmDir                 string
	// Linux-specific flags
	kernelPath string
	initrdPath string
	cmdLine    string
	isoPath    string
	// Verbose flag
	verbose bool
	// Optional pprof listener for live diagnostics.
	pprofAddr string
	// Remote fleet context.
	fleetName string
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
	// Print version and exit
	showVersion bool
	// Shared directory path for VirtioFS (deprecated, use -v)
	shareDir string
	// Volume mounts (docker-style -v flag)
	volumes volumeSlice
	// Provisioning flags
	provisionUser     string
	provisionPassword string
	provisionAdmin    bool
	provisionStrategy string
	noAgent           bool
	// VM selection flag
	vmName string
	// Clone options
	cloneLinked bool
	// Disposable run mode; boots from a temporary linked clone.
	disposableMode bool
	// Ephemeral fork mode for local image refs. The older VM-parent
	// RAM-overlay path is rejected with an actionable error until the
	// temporary RAM storage attachment path is implemented.
	ephemeralForkParent string
	ephemeralForkName   string
	ephemeralForkKeep   bool
	// runEphemeral marks an image-fork-from child for destroy-on-stop
	// using the .ephemeral sentinel from fork_ephemeral.go. Slice 1 of
	// design 024.
	runEphemeral bool
	// Network mode (nat, bridged:<iface>, vmnet, none)
	networkMode string
	// Sandbox policy for safer research runs.
	sandboxLevel string
	// USB storage devices
	usbDevices USBStorageSlice
	// Raw block devices
	blockDevices blockDeviceSlice
	// Display configurations
	displays displayx.Slice
	// Rosetta for Linux VMs
	enableRosetta bool
	// Clipboard sharing (SPICE agent)
	enableClipboard bool
	// Experimental Windows VM graphics device.
	windowsGraphicsMode string
	// Experimental Windows serial port device.
	windowsSerialMode string
	// Experimental Windows EFI ROM image.
	windowsEFIRomPath string
	// vzscripts to run after install (comma-separated recipe names)
	installVZScripts string
	// Headless mode (disables GUI)
	headlessMode bool
	// Unattended install flags
	unattended               bool
	bootCommandsFile         string
	debugOCR                 bool
	automationBackend        string
	automationCaptureBackend string
	automationInputBackend   string
	// Auto-mount tagged volumes via agent
	autoMountVolumes bool
	// Auto-upgrade guest agent when version mismatches host
	autoUpgradeAgent bool
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
	// Private VNC server port or :port.
	vncAddress string
	// Optional PCAP output when using -network filehandle.
	pcapPath string
	// Optional disk synchronization override for disk-image attachments.
	diskSyncMode string
	// Private VNC password.
	vncPassword string
	// Optional Bonjour service name for private VNC.
	vncBonjourService string
	// Private GDB debug-stub port or :port.
	gdbAddress string
	// Listen on all interfaces for the GDB debug stub.
	gdbListenAll bool
	// Private save options for suspend/resume state.
	saveCompress bool
	saveEncrypt  bool
	// Private macOS boot-stop options.
	forceDFU          bool
	stopInIBootStage1 bool
	stopInIBootStage2 bool
	// HTTP listener address for cove run -http.
	runHTTPAddr string
	// Maximum time to wait for a VM to report Running during start.
	startTimeout time.Duration
	// Startup host TCP -> guest vsock forwards.
	startupPortForwards portForwardSpecs
)

func init() {
	flag.Usage = usage
	flag.BoolVar(&fetchLatest, "fetch-latest", false, "fetch latest supported restore image info")
	flag.BoolVar(&runVM, "run", false, "deprecated: use the run command")
	flag.BoolVar(&installVM, "install", false, "deprecated: use the install command")
	flag.BoolVar(&guiMode, "gui", true, "show VM display in a window")
	flag.BoolVar(&headlessMode, "headless", false, "run without GUI window")

	flag.BoolVar(&linuxMode, "linux", false, "run a Linux VM instead of macOS")
	flag.BoolVar(&windowsMode, "windows", false, "run a Windows ARM64 VM instead of macOS (experimental)")
	flag.BoolVar(&linuxDesktop, "desktop", false, "use Ubuntu Desktop ISO (implies -linux)")
	flag.BoolVar(&nixosMode, "nixos", false, "install or run a NixOS Linux VM")
	flag.StringVar(&linuxDistro, "distro", "ubuntu", "Linux distro: ubuntu, debian, fedora, alpine, nixos")
	flag.BoolVar(&linuxNested, "nested", false, "enable nested virtualization for Linux guests (M3/M4 on macOS 15+)")
	flag.BoolVar(&linuxNVMe, "nvme", false, "attach Linux root disk through NVMe instead of virtio-blk")
	flag.StringVar(&linuxDesktopInstaller, "desktop-installer", "oem", "ubuntu desktop install path: 'oem' (Desktop ISO autoinstall) or 'server' (boot Server ISO + apt install ubuntu-desktop)")
	flag.BoolVar(&linuxShell, "shell", false, "after Linux guest boots, attach the host terminal to a guest shell via the agent (requires -linux; mutually exclusive with -headless)")
	flag.BoolVar(&verbose, "verbose", false, "verbose output (includes run loop debugging)")
	flag.StringVar(&pprofAddr, "pprof", "", "serve net/http/pprof on localhost for diagnostics (for example 6060 or localhost:6060)")
	flag.StringVar(&fleetName, "fleet", "", "remote fleet context")
	flag.UintVar(&cpuCount, "cpu", 2, "number of CPUs")
	flag.Uint64Var(&memoryGB, "memory", 4, "memory in GB")
	flag.StringVar(&diskPath, "disk", "", "path to disk image")
	flag.Uint64Var(&diskSizeGB, "disk-size", 64, "disk size in GB for new disk images")
	flag.BoolVar(&rawDisk, "raw-disk", false, "preallocate new install disk images")
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
	flag.BoolVar(&listVMs, "list", false, "deprecated: use the list command")
	flag.BoolVar(&showVersion, "version", false, "print version information and exit")
	flag.Var(&volumes, "v", "alias for -vol")
	flag.Var(&volumes, "vol", "mount host directory: /host/path[:tag][:ro|rw][:opt=val,...] (repeatable; default tag is the host directory name)")
	flag.StringVar(&shareDir, "share-dir", "", "deprecated alias for -vol /host/path")
	flag.StringVar(&provisionUser, "provision-user", "", "username for auto-provisioned user (enables provisioning)")
	flag.StringVar(&provisionPassword, "provision-password", "", "password for auto-provisioned user")
	flag.BoolVar(&provisionAdmin, "provision-admin", true, "make auto-provisioned user an admin")
	flag.StringVar(&provisionStrategy, "provision-strategy", "disk",
		"provisioning strategy: disk (mount disk + write files, needs admin), gui (keyboard automation), auto (try disk, fall back to gui)")
	flag.BoolVar(&noAgent, "no-agent", false, "skip vz-agent installation during Linux install/provisioning")
	flag.StringVar(&installVZScripts, "vzscripts", "", "comma-separated vzscript recipes to run after install (e.g. homebrew,openclaw)")
	// VM selection flag
	flag.StringVar(&vmName, "vm", "", "VM name to use (default: active VM or 'default')")
	// Clone options
	flag.BoolVar(&cloneLinked, "linked", false, "create linked clone using APFS copy-on-write")
	flag.BoolVar(&disposableMode, "disposable", false, "run from a disposable linked clone and delete it after shutdown")
	// Ephemeral fork (design 013 Phase 3)
	flag.StringVar(&ephemeralForkParent, "fork-from", "", "boot a fork from a local image ref; VM-parent RAM-overlay forks are not implemented")
	flag.StringVar(&ephemeralForkName, "fork-name", "", "explicit name for the forked VM")
	flag.BoolVar(&ephemeralForkKeep, "keep", false, "with -fork-from, retain the ephemeral vmDir after exit")
	flag.BoolVar(&runEphemeral, "ephemeral", false, "with -fork-from <image-ref>, destroy the materialized child on stop and skip vm tree registration")
	// Network mode
	flag.StringVar(&networkMode, "network", "nat", "network mode: nat, bridged:<iface>, vmnet, filehandle, none")
	flag.StringVar(&networkMode, "net", "nat", "alias for -network")
	flag.Var(&startupPortForwards, "port-forward", "forward host TCP to guest vsock: hostPort:guestVsockPort (repeatable)")
	flag.Var(&startupPortForwards, "pf", "alias for -port-forward")
	flag.StringVar(&sandboxLevel, "sandbox-level", "", "research isolation policy: minimal or strict")
	flag.StringVar(&proxyURL, "proxy", "", "configure guest system HTTP/HTTPS proxy after boot (for example http://192.168.64.1:8080)")
	flag.StringVar(&pcapPath, "pcap", "", "write captured Ethernet frames to a PCAP file when using -network filehandle")
	flag.StringVar(&diskSyncMode, "disk-sync", "", "disk image synchronization override: fsync, none, or full")
	// USB storage
	flag.Var(&usbDevices, "usb", "USB storage device: /path/to/disk.img[:ro] (can be repeated)")
	flag.Var(&blockDevices, "block", "raw block device: /dev/rdiskN:ro|rw[:sync=full|none] (can be repeated)")
	// Display configuration
	flag.Var(&displays, "display", "display config: WIDTHxHEIGHT[@PPI] or preset (4k, 1080p, 720p)")
	// Rosetta for Linux
	flag.BoolVar(&enableRosetta, "rosetta", true, "enable Rosetta translation support when running Linux VMs")
	// Clipboard sharing
	flag.BoolVar(&enableClipboard, "clipboard", true, "enable host↔guest clipboard sharing via SPICE agent (requires spice-vdagent in guest; macOS 15+ for macOS guests)")
	flag.StringVar(&windowsGraphicsMode, "windows-graphics", "virtio", "Windows graphics mode: virtio or linear-framebuffer")
	flag.StringVar(&windowsSerialMode, "windows-serial", "virtio", "Windows serial port: virtio, pl011, or 16550")
	flag.StringVar(&windowsEFIRomPath, "windows-efi-rom", "", "Windows EFI ROM image for private VZEFIBootLoader experiment")
	flag.BoolVar(&skipResume, "no-resume", false, "discard saved suspend state and perform a cold boot")
	flag.BoolVar(&skipResume, "cold-boot", false, "same as -no-resume")
	flag.StringVar(&launchOrder, "launch-order", "window-first", "GUI launch order: window-first or start-first")
	flag.StringVar(&runtimeProfile, "runtime-profile", "full", "macOS runtime profile: full or minimal")
	flag.BoolVar(&appleLog, "apple-log", false, "stream Apple unified logs relevant to virtualization while running")
	flag.StringVar(&appleLogPredicate, "apple-log-predicate", "", "custom predicate for -apple-log (NSPredicate syntax)")
	flag.BoolVar(&recoverIdentity, "recover-identity", false, "if VM identity metadata is missing, back it up and reset identity files to attempt recovery")
	flag.StringVar(&vncAddress, "vnc", "", "start private VNC server on port or :port (for example :5901)")
	flag.StringVar(&vncPassword, "vnc-password", "", "password for private VNC server")
	flag.StringVar(&vncBonjourService, "vnc-bonjour", "", "bonjour service name for the private VNC server")
	flag.StringVar(&gdbAddress, "gdb", "", "attach private GDB debug stub on port or :port (for example :1234)")
	flag.BoolVar(&gdbListenAll, "gdb-listen-all", false, "listen on all interfaces for -gdb")
	flag.BoolVar(&saveCompress, "save-compress", false, "compress suspend state using private save options")
	flag.BoolVar(&saveEncrypt, "save-encrypt", false, "encrypt suspend state using private save options")
	flag.BoolVar(&forceDFU, "force-dfu", false, "start a macOS VM in DFU mode using private start options")
	flag.BoolVar(&stopInIBootStage1, "iboot-stage1", false, "start a macOS VM and stop in iBoot stage 1")
	flag.BoolVar(&stopInIBootStage2, "iboot-stage2", false, "start a macOS VM and stop in iBoot stage 2")
	flag.StringVar(&runHTTPAddr, "http", "", "expose VM HTTP API on TCP address (e.g. 127.0.0.1:7777 or :0 for random port)")
	flag.DurationVar(&startTimeout, "start-timeout", 60*time.Second, "maximum time to wait for VM startup before failing")
	// Unattended install
	flag.BoolVar(&unattended, "unattended", false, "fully unattended install + setup (disk provisioning, OCR fallback)")
	flag.StringVar(&bootCommandsFile, "boot-commands", "", "path to vzscript automation file for custom setup")
	flag.BoolVar(&debugOCR, "debug-ocr", false, "save OCR debug screenshots with text bounding boxes")
	flag.StringVar(&automationBackend, "automation-backend", "auto", "UI automation backend: auto, framebuffer, or window")
	flag.StringVar(&automationCaptureBackend, "automation-capture-backend", "", "override screenshot backend: auto, framebuffer, or window")
	flag.StringVar(&automationInputBackend, "automation-input-backend", "", "override input backend: auto, direct, or window")
	// Auto-mount volumes
	flag.BoolVar(&autoMountVolumes, "auto-mount-volumes", true, "auto-mount tagged volumes in guest via agent")
	flag.BoolVar(&autoUpgradeAgent, "auto-upgrade-agent", false, "auto-upgrade guest agent when version mismatches host")
	// Force install (skip existing VM check)
	flag.BoolVar(&forceInstall, "force", false, "force install even if VM disk already exists (DESTROYS existing data)")
}

func main() {
	// Hidden re-exec entrypoint used by AuthorizationExecuteWithPrivileges.
	// AEWP only grants the launched tool permission to call setuid(0), it
	// does not run it as root. We re-exec cove itself with a typed manifest
	// (no arbitrary script execution path). Must run before flag.Parse so
	// the hidden subcommand isn't misinterpreted as a flag.
	maybeRunElevatedOp()

	flag.Parse()

	if showVersion {
		fmt.Fprintln(newCommandEnv().Stdout, versionInfo())
		return
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)).With(slog.String("component", "cove")))

	maybeStartPprofServer()

	// -desktop and -nested imply -linux
	if linuxDesktop || linuxNested || nixosMode {
		linuxMode = true
	}
	if nixosMode {
		linuxDistro = "nixos"
	}
	if windowsMode && linuxMode {
		fmt.Fprintf(os.Stderr, "error: -windows and -linux are mutually exclusive\n")
		os.Exit(1)
	}
	if linuxMode {
		if _, err := parseLinuxVariant(linuxDistro, linuxDesktop); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	}
	if err := validateInstallMediaPaths(flag.Args()); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	cpuExplicit = flagWasProvided(flag.CommandLine, "cpu")
	applyNestedLinuxDefaults()

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

	if handled, exitCode := handleEarlyCLI(flag.Args()); handled {
		if exitCode != 0 {
			os.Exit(exitCode)
		}
		return
	}
	if flag.NArg() > 0 {
		if spec, ok := lookupCommand(flag.Arg(0)); ok && spec.Dispatch == commandDispatchPreUI {
			os.Exit(runRegisteredCommand(newCommandEnv(), spec, flag.Arg(0), flag.Args()[1:]))
		}
	}

	// Set up macgo bundling (entitlements, signing, app icon).
	// Must be before LockOSThread. May relaunch and not return.
	initMacgo()

	runtime.LockOSThread()
	registerUIThread()

	// Note: NSSetUncaughtExceptionHandler disabled — purego cannot marshal
	// struct types through callbacks (NSException is a Go struct wrapper).
	// ObjC exceptions will still surface as Go panics via the runtime.
	// foundation.NSSetUncaughtExceptionHandler(func(e foundation.NSException) {
	// 	panic("Exiting due to uncaught exception.")
	// })
	// Resolve VM directory using registry (handles migration and VM selection).
	// Skip for subcommands that don't operate on a specific VM — notably
	// `helper daemon`, which runs as root via launchd. As root, $HOME is
	// /var/root, which is on the SIP-sealed root volume (EROFS).
	var err error
	if !subcommandSkipsVMDir(flag.Args()) {
		vmDir, err = vmconfig.EnsureDir(vmName, vmDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		// Load saved VM config and apply defaults for flags not explicitly set.
		applyVMConfig(vmDir)
	}
	if err := applySandboxDefaults(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

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

	// Legacy flag handling compatibility
	if fetchLatest {
		if _, err := fetchLatestRestoreImageObject(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if listVMs {
		env := newCommandEnv()
		if err := handleListTo(env.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if installVM {
		os.Exit(runLegacyInstallFlag())
	}
	if utmBundlePath != "" {
		handleUTM()
		return
	}
	if runVM {
		os.Exit(runLegacyRunFlag())
	}
	if flag.NArg() > 0 {
		cmd := flag.Arg(0)
		args := flag.Args()[1:]
		if fleetName != "" && cmd != "fleet" {
			if err := handleFleetRoute(context.Background(), fleetName, cmd, args); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			return
		}

		if spec, ok := lookupCommand(cmd); ok && spec.Dispatch == commandDispatchEarly {
			os.Exit(runRegisteredCommand(newCommandEnv(), spec, cmd, args))
		}

		// Re-parse remaining args so flags after the subcommand work
		// (e.g., "cove run -gui" parses -gui here).
		flag.CommandLine.Parse(args)

		if linuxDesktop || linuxNested || linuxNVMe || nixosMode {
			linuxMode = true
		}
		if nixosMode {
			linuxDistro = "nixos"
		}
		if windowsMode && linuxMode {
			fmt.Fprintf(os.Stderr, "error: -windows and -linux are mutually exclusive\n")
			os.Exit(1)
		}
		if linuxMode {
			if _, err := parseLinuxVariant(linuxDistro, linuxDesktop); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
		}
		cpuExplicit = flagWasProvided(flag.CommandLine, "cpu")
		applyNestedLinuxDefaults()

		// --headless overrides --gui after subcommand re-parse
		if headlessMode {
			guiMode = false
		}
		if err := applySandboxDefaults(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if err := validateLaunchOptions(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if err := validateInstallMediaPaths([]string{cmd}); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		// Re-resolve vmDir if -vm flag was provided after subcommand.
		if code := rerunVMDirForPostCommand(cmd, args); code != 0 {
			os.Exit(code)
		}

		if spec, ok := lookupCommand(cmd); ok && spec.Dispatch == commandDispatchLate {
			os.Exit(runRegisteredCommand(newCommandEnv(), spec, cmd, args))
		}
		if s := suggestCommand(cmd); s != "" {
			fmt.Fprintf(os.Stderr, "cove: unknown command %q. Did you mean %q?\n", cmd, s)
		} else {
			fmt.Fprintf(os.Stderr, "cove: unknown command %q.\nRun 'cove -help' for usage.\n", cmd)
		}
		os.Exit(2)
	}

	// Default: smart routing based on number of VMs
	handleDefaultAction()
}

func validateInstallMediaPaths(args []string) error {
	if !installCommandRequested(args) {
		return nil
	}
	ipsw, iso, err := installMediaPathArgs(args)
	if err != nil {
		return err
	}
	if strings.TrimSpace(ipsw) != "" {
		src, err := parseIPSWSource(ipsw)
		if err != nil {
			return fmt.Errorf("install: %w", err)
		}
		if !src.IsURL {
			if err := validateReadableFile(src.Path); err != nil {
				return fmt.Errorf("install: ipsw path %s: %w", src.Path, err)
			}
		}
	}
	if strings.TrimSpace(iso) != "" && !isURL(iso) {
		if err := validateReadableFile(iso); err != nil {
			return fmt.Errorf("install: iso path %s: %w", iso, err)
		}
	}
	return nil
}

func installMediaPathArgs(args []string) (ipsw, iso string, err error) {
	ipsw, iso = ipswPath, isoPath
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-ipsw" || arg == "--ipsw":
			i++
			if i >= len(args) {
				return "", "", fmt.Errorf("install: %s requires a path", arg)
			}
			ipsw = args[i]
		case strings.HasPrefix(arg, "-ipsw="):
			ipsw = strings.TrimPrefix(arg, "-ipsw=")
		case strings.HasPrefix(arg, "--ipsw="):
			ipsw = strings.TrimPrefix(arg, "--ipsw=")
		case arg == "-iso" || arg == "--iso":
			i++
			if i >= len(args) {
				return "", "", fmt.Errorf("install: %s requires a path", arg)
			}
			iso = args[i]
		case strings.HasPrefix(arg, "-iso="):
			iso = strings.TrimPrefix(arg, "-iso=")
		case strings.HasPrefix(arg, "--iso="):
			iso = strings.TrimPrefix(arg, "--iso=")
		}
	}
	return ipsw, iso, nil
}

func installCommandRequested(args []string) bool {
	if installVM {
		return true
	}
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "install", "up":
		return true
	default:
		return false
	}
}

func validateReadableFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("is a directory")
	}
	return nil
}

// handleDefaultAction routes based on the selected UI mode:
//   - GUI mode: show the native selector, including its empty-state New VM flow
//   - non-GUI mode with 0 VMs: start guided install
//   - non-GUI mode with 1 VM: run it directly
//   - non-GUI mode with 2+ VMs: show native VM selector window
func handleDefaultAction() {
	vms, err := vmconfig.List(detectVMState)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: listing VMs: %v\n", err)
		os.Exit(1)
	}
	if len(vms) == 0 {
		if info, err := vmconfig.InfoFor(vmDir, detectVMState); err == nil {
			vms = append(vms, *info)
		}
	}
	if guiMode {
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
		} else if vms[0].OSType == "Windows" {
			windowsMode = true
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
	if err := runCurrentVM(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func handleActionCommand(args []string) int {
	if len(args) == 0 || isHelpArg(args[0]) {
		printActionUsage(os.Stderr)
		return 0
	}
	switch args[0] {
	case "doctor":
		return action.RunDoctorCommand(context.Background(), args[1:], os.Stdout, os.Stderr)
	case "prepare-image":
		return action.RunPrepareCommand(context.Background(), args[1:], os.Stdout, os.Stderr)
	default:
		fmt.Fprintf(os.Stderr, "unknown action command: %s\n", args[0])
		return 1
	}
}

func printActionUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove action <command> [options]

Preflight helpers for private GitHub Actions runner images.

Commands:
  doctor          Check host-side cove action prerequisites
  prepare-image   Validate a local image or running VM for runner use`)
}

func usage() {
	fmt.Fprintf(os.Stderr, `cove - Apple Virtualization Framework Example

Usage:
  cove [flags] [command]

Use 'cove <command> -h' for command-specific help.

Quick Start:
  up              Install + provision + boot in one command (cove up -user me)

Cirrus Migration:
  See docs/quickstart-from-cirrus.md and docs/migrations/from-cirrus.md

Lifecycle:
  install         Install OS (macOS from IPSW, -linux for Ubuntu)
  run             Run a VM (macOS by default, -linux for Linux)
  list            List available VMs and templates
  clean           Remove VM files (disk, aux, hw.model, machine.id)

Provisioning:
  provision       Write provisioning files into VM disk (self-contained)
  provision-agent Provision vz-agent daemon (idempotent; uses running-VM vsock when possible)
  agent-upgrade   Live-upgrade vz-agent in a running VM (build, copy, restart)
  verify          Verify provisioning files in VM disk (alias: doctor)
  sip             SIP management (enable/disable/status + recovery automation)

VM Management:
  vm set <name>           Set active VM
  vm delete <name>        Delete a VM (aliases: rm, remove, destroy)
  vm rename <old> <new>   Rename a VM (alias: rename)
  vm export <name> <path> Export VM to tarball (alias: export)
  vm import <path> <name> Import VM from tarball (alias: import)
  vm config ...           Export/import framework config snapshots (alias: config)
  vm tree                 Print fork lineage
  clone           Clone a VM (cove clone [source] <target> [--linked])
  fork            CoW-fork a VM with a fresh identity (cove fork <parent> <child>)
  image           Local VM image store (build/list/rm); see 'cove image -h'
  diff            Compare local image disk layer metadata
  softreset       Run destructive soft-reset probe matrix
  runs            Inspect local run metrics and artifacts
  bench           Normalize benchmark evidence into reports and run metrics
  daemon          Manage the cove background coordinator
  compact         Zero guest free space for smaller pushes
  fleet           Register and use remote cove hosts
  build           Chain vzscript steps into a cache-keyed VM image
  action          Preflight helpers for private GitHub Actions runner images
  push            Plan a VM disk OCI push (dry-run)
  pull            Validate an OCI pull plan (dry-run)
  policy          VM lifecycle policy (idle timeout, max age, run budget)
  store           Manage the local OCI blob store
  gc              Delete old disposable VM clones
  template        Manage VM templates (save/list/create)

Shared Folders:
  shared-folder list                    List configured shared folders
  shared-folder add <path> [tag] [ro]   Add a shared folder
  shared-folder remove <tag-or-path>    Remove a shared folder
  shared-folder status                  Show mount status in guest
  shared-folder mount                   Mount in guest via agent

Snapshots:
  snapshot        Manage VM state snapshots (list/save/restore/delete)
  pit             Experimental point-in-time save, restore, run, and swap
  disk-snapshot   Manage disk-level snapshots (APFS clonefile, COW)

HTTP & MCP:
  serve           Multi-VM HTTP gateway on localhost:7777 (cove serve -h for options)
  run -http :7777 Per-VM HTTP API alongside the Unix socket

Runtime Control:
  ctl             Control running VM via socket (screenshot, key, text, mouse, ...)
  cp              Copy files between host and guest (cove cp host vm:/path)
  forward         Forward host TCP to guest TCP (cove forward vm 8080:80)
  quota           Show or set per-VM resource quotas
  ctl disk list   Inspect runtime storage devices
  ctl usb list    Inspect runtime USB controllers and devices
  logs            Show guest logs from a running VM (cove logs <vm> [-f])
  shell           Open a Docker-shaped exec session in a running VM (cove shell <vm>)
  agent-sandbox   Run a computer-use provider loop in a fresh VM fork
  vzscript        Run guest-agent and UI automation scripts (rsc.io/script + txtar)
  run -headless -vnc :5901            Expose a private VNC console
  run -gdb :1234                      Attach a private GDB debug stub
  run -sandbox-level strict -disposable -gui
                                      Safer disposable analysis run
  run -network filehandle -pcap /tmp/vm.pcap
                                      Capture raw guest Ethernet frames

Networking:
  network         Network configuration (list interfaces, help)
  rosetta         Rosetta 2 for Linux VMs (status/install/setup)

Other:
  disk-detach     Detach VM disk if stuck from a previous provision/verify
  help [command]  Show top-level or command-specific help
  version         Print version information

Auto-Provisioning (Recommended - provision command):
  Write user provisioning directly into VM disk (no VirtioFS needed):

  cove install -ipsw restore.ipsw
  cove provision -user testuser -skip-setup-assistant  # prompts for password
  cove run

  This creates a self-contained LaunchDaemon that:
  - Runs on first boot to create the user account
  - Skips Setup Assistant entirely (with -skip-setup-assistant)
  - Self-cleans after execution

Auto-Provisioning (Alternative - GUI automation):
  Use -provision-user with -gui to automate user creation (prompts for password):

  cove run -gui -provision-user testuser

  This will:
  1. Start the VM with GUI window
  2. Detect when Setup Assistant appears
  3. Navigate through setup using keyboard automation
  4. Create the specified user account
  5. Proceed to desktop

Provisioning Strategy (-provision-strategy):
  disk (default)    Stop VM after install, mount disk, write LaunchDaemon.
                    On first boot, launchd creates user. Needs admin.
  gui               Skip disk provisioning. On first boot, navigate Setup
                    Assistant via keyboard automation. No admin needed.
  auto              Try disk first. If it fails, fall back to gui.

Linux VM:
	  Install and run Ubuntu, Debian, Fedora, or Alpine ARM64:

	  cove install -linux                                    # Ubuntu Server (default)
	  cove install -linux -distro debian                     # Debian
	  cove install -linux -distro fedora                     # Fedora
	  cove install -linux -distro alpine                     # Alpine
	  cove run -linux -nested                                # KVM on supported hosts
	  cove install -linux -desktop                           # Ubuntu Desktop
  cove install -linux -iso /path/to/ubuntu.iso           # Use local ISO
  cove install -linux -provision-user me -provision-password pw  # With user
  cove run -linux                                        # Run installed VM
  cove run -linux -gui                                   # Run with display
  cove up -linux -user me                                # Server: install + boot
  cove up -linux -desktop -user me                       # Desktop: install + boot

Windows VM (experimental):
  cove install -windows -iso /path/to/Win11_ARM64.iso
  cove run -windows
  cove run -windows -windows-graphics linear-framebuffer # use private framebuffer experiment

Volume Mounting (-vol flag):
  Docker-style volume mounts. Format: /host/path[:tag][:ro|rw][:opt=val,...]

  If tag is omitted, the guest tag defaults to the host directory name.
  On macOS guests, tagged mounts are auto-mounted at /Volumes/<tag>.
  On Linux guests, tagged mounts are auto-mounted at /mnt/<tag> with the
  provisioned user's uid/gid (default 1000:1000) for writable host files.
  '/Volumes/My Shared Files' is the shared-folder flow, not the -vol flow.
  Parts containing "=" are guest mount options; they are primarily useful on Linux.

  Examples:
    -vol ~/code                                Tag defaults to "code"
    -vol ~/code:code:ro                        Mount at /Volumes/code (read-only)
    -vol /path/to/dir:MyData                   Mount at /Volumes/MyData (rw)
    -vol /path/to/dir:MyData:ro                Mount at /Volumes/MyData (read-only)
    -vol /path/to/dir:MyData:cache=metadata    Override Linux VirtioFS cache mode (default: cache=none)
    -vol ~/code:Code -vol ~/data:Data          Multiple volumes

Flags:
`)
	printCommandDefaults(os.Stdout, flag.CommandLine)
}

func printCommandDefaults(w *os.File, fs *flag.FlagSet) {
	fs.VisitAll(func(f *flag.Flag) {
		if f.Name == "disk-sync" {
			return
		}
		if f.Name == "nvme" {
			return
		}
		if f.Name == "raw-disk" {
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
}

func validateLaunchOptions() error {
	if windowsMode && linuxMode {
		return fmt.Errorf("-windows and -linux are mutually exclusive")
	}
	if linuxNested && !linuxMode {
		return fmt.Errorf("-nested requires -linux")
	}
	if linuxNVMe && !linuxMode {
		return fmt.Errorf("-nvme requires -linux")
	}
	if linuxDesktop && windowsMode {
		return fmt.Errorf("-desktop requires -linux")
	}
	if linuxShell {
		if !linuxMode {
			return fmt.Errorf("-shell requires -linux")
		}
		if headlessMode {
			return fmt.Errorf("-shell is mutually exclusive with -headless (the host terminal is used for the shell)")
		}
	}
	if len(blockDevices) > 0 {
		if !linuxMode {
			return fmt.Errorf("-block requires -linux")
		}
		if !helperInstalled() {
			return fmt.Errorf("block devices require an up-to-date cove-helper; run: sudo cove helper install")
		}
		fresh, _, err := helperBinaryFreshness()
		if err != nil {
			return fmt.Errorf("check cove-helper freshness: %w", err)
		}
		if !fresh {
			return fmt.Errorf("block devices require an up-to-date cove-helper; run: sudo cove helper install")
		}
	}
	switch provisionStrategy {
	case "disk", "gui", "auto":
	case "inject":
		// Accept old name as alias.
		provisionStrategy = "disk"
	default:
		return fmt.Errorf("invalid -provision-strategy %q (must be disk, gui, or auto)", provisionStrategy)
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
	if _, err := parseWindowsGraphicsMode(windowsGraphicsMode); err != nil {
		return err
	}
	if _, err := parseAutomationBackend(automationBackend); err != nil {
		return err
	}
	if strings.TrimSpace(automationCaptureBackend) != "" {
		if _, err := parseAutomationCaptureBackend(automationCaptureBackend); err != nil {
			return err
		}
	}
	if strings.TrimSpace(automationInputBackend) != "" {
		if _, err := parseAutomationInputBackend(automationInputBackend); err != nil {
			return err
		}
	}
	if diskSizeGB < 1 {
		return fmt.Errorf("disk-size must be at least 1 (GB)")
	}
	if diskSizeGB > 16384 {
		return fmt.Errorf("disk-size must be at most 16384 (16 TB)")
	}
	if cpuCount < 1 {
		return fmt.Errorf("cpu must be at least 1")
	}
	if cpuCount > 256 {
		return fmt.Errorf("cpu must be at most 256")
	}
	if memoryGB < 1 {
		return fmt.Errorf("memory must be at least 1 (GB)")
	}
	if memoryGB > 512 {
		return fmt.Errorf("memory must be at most 512 (GB)")
	}
	if _, err := ParseSandboxLevel(sandboxLevel); err != nil {
		return err
	}
	if strings.TrimSpace(pcapPath) != "" && strings.TrimSpace(networkMode) != "filehandle" {
		return fmt.Errorf("-pcap requires -network filehandle")
	}
	if err := validateNetworkMode(networkMode); err != nil {
		return err
	}
	if err := validateProxyFlags(); err != nil {
		return err
	}
	if err := validatePrivateRuntimeOptions(); err != nil {
		return err
	}
	return nil
}

func flagWasProvided(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func applyNestedLinuxDefaults() {
	if linuxNested && !cpuExplicit && cpuCount < 4 {
		cpuCount = 4
	}
}

func confirmDeletef(format string, args ...any) (bool, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return true, nil
	}
	fmt.Fprintf(os.Stderr, format, args...)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false, fmt.Errorf("read confirmation: %w", err)
	}
	answer := strings.TrimSpace(line)
	return answer == "y" || answer == "Y", nil
}

func handleListTo(stdout io.Writer) error {
	// List VMs
	vms, err := vmconfig.List(detectVMState)
	if err != nil {
		return fmt.Errorf("listing VMs: %w", err)
	}

	activeVM := vmconfig.ActiveName()

	if len(vms) == 0 {
		fmt.Fprintln(stdout, "No VMs found. Run 'cove install' to create one.")
	} else {
		fmt.Fprintln(stdout, "VMs:")
		w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "  NAME\tOS\tSTATE\tUPTIME\tNOTE\tSIZE\tCREATED\tACTIVE")
		for _, vm := range vms {
			active := ""
			if vm.Name == activeVM {
				active = "*"
			}
			uptime, note := runtimeListFields(vm.Path, vm.State)
			fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				vm.Name,
				vm.OSType,
				vm.State,
				uptime,
				note,
				bytefmt.Size(vm.DiskSize),
				vm.Created.Format("2006-01-02"),
				active)
		}
		if err := w.Flush(); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "GUI state: cove ctl -vm <name> gui status")
	}

	// List templates
	templates, err := ListTemplates()
	if err == nil && len(templates) > 0 {
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, "Templates:")
		w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "  NAME\tSIZE\tCREATED")
		for _, t := range templates {
			fmt.Fprintf(w, "  %s\t%s\t%s\n",
				t.Name,
				bytefmt.Size(t.DiskSize),
				t.Created.Format("2006-01-02"))
		}
		if err := w.Flush(); err != nil {
			return err
		}
	}

	// Surface orphan VM directories (dirs without a valid disk image)
	// so users can clean them up with `cove vm delete <name>`.
	if orphans, err := vmconfig.ListOrphans(); err == nil && len(orphans) > 0 {
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, "Missing-disk VM directories hidden from the main list:")
		for _, name := range orphans {
			fmt.Fprintf(stdout, "  %s\t(no disk image found)\n", name)
		}
		fmt.Fprintln(stdout, "These are filesystem cleanup entries, not fork-lineage orphans from vm tree --orphans.")
		fmt.Fprintln(stdout, "Remove with: cove vm delete <name>")
	}
	return nil
}

func runtimeListFields(vmPath, state string) (string, string) {
	if state != "starting" {
		return "-", "-"
	}
	rt, err := readVMRuntimeState(vmPath)
	if err != nil || rt.PID <= 0 || !processLive(rt.PID) {
		return "-", "-"
	}
	note := strings.TrimSpace(rt.Phase)
	if note == "" {
		note = "starting"
	}
	note = fmt.Sprintf("%s (pid=%d)", note, rt.PID)
	since := rt.LastPhaseAt
	if since.IsZero() {
		since = rt.UpdatedAt
	}
	if since.IsZero() {
		return "-", note
	}
	return shortDuration(time.Since(since)), note
}

func shortDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	if d < time.Minute {
		return d.String()
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%02ds", int(d/time.Minute), int(d%time.Minute/time.Second))
	}
	return fmt.Sprintf("%dh%02dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
}

// handleClone handles the clone subcommand.
func handleClone(args []string) {
	if err := runClone(args); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

var cloneProvisionAgentForVM = provisionAgentForVM

func runClone(args []string) error {
	// Parse: clone [source] <target> [--linked] [--with-agent]
	var source, target string
	withAgent := false

	nonFlagArgs := []string{}
	for _, arg := range args {
		switch arg {
		case "--linked", "-linked":
			cloneLinked = true
		case "--with-agent", "-with-agent":
			withAgent = true
		default:
			if len(arg) > 0 && arg[0] != '-' {
				nonFlagArgs = append(nonFlagArgs, arg)
			}
		}
	}

	switch len(nonFlagArgs) {
	case 0:
		return fmt.Errorf("usage: cove clone [source] <target> [--linked] [--with-agent]")
	case 1:
		source = vmconfig.ActiveName()
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
		return err
	}
	fmt.Printf("Clone created: %s\n", target)

	if withAgent {
		fmt.Println()
		fmt.Println("=== Provisioning agent into clone ===")
		if err := cloneProvisionAgentForVM(vmSelection{
			Directory: vmconfig.Path(target),
			Name:      target,
		}); err != nil {
			if cloneAgentProvisionSkippedForLinux(err) {
				fmt.Fprintf(os.Stderr, "note: clone %q is Linux; offline agent injection is not supported\n", target)
				fmt.Fprintln(os.Stderr, "      the clone keeps any agent already installed inside the source VM")
				fmt.Fprintf(os.Stderr, "      if the agent is missing, boot the VM and install it from inside the guest: cove -vm %s provision-agent\n", target)
				return nil
			}
			fmt.Fprintf(os.Stderr, "warning: clone %q was created, but --with-agent provisioning failed: %v\n", target, err)
			fmt.Fprintf(os.Stderr, "         retry after the backup is selected with: cove -vm %s provision -agent\n", target)
			return nil
		}
	}
	return nil
}

func cloneAgentProvisionSkippedForLinux(err error) bool {
	return err != nil && strings.Contains(err.Error(), "offline agent inject is not supported for Linux VMs")
}

// handleFork handles the fork subcommand: creates a CoW clone of an
// existing VM with a fresh machine identity. See ForkVM and
// ForkVMWithSnapshot in fork.go.
//
// Two CLI surfaces, sharing one implementation:
//
//	cove fork <parent> <child> [-snapshot <name>]
//	cove fork --from <parent[@snapshot]> <child> [-snapshot <name>]
//
// When both --from and -snapshot are given, their snapshot must agree
// (or --from must omit @ and let -snapshot fill in).
func handleFork(args []string) {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" || arg == "help" {
			printForkUsage(os.Stdout)
			return
		}
	}
	flagArgs, posArgs, err := splitForkArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fs := flag.NewFlagSet("fork", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		fromRef  string
		snapshot string
	)
	fs.StringVar(&fromRef, "from", "", "fork from parent[@snapshot] (alternative to positional <parent>)")
	fs.StringVar(&snapshot, "snapshot", "", "seed child suspend.vmstate from parent's named snapshot")
	fs.Usage = func() { printForkUsage(os.Stderr) }
	if err := fs.Parse(flagArgs); err != nil {
		os.Exit(2)
	}

	parent, child, snap, err := resolveForkInvocation(fromRef, snapshot, posArgs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if err := ForkVMWithSnapshot(ForkVMOptions{Parent: parent, Child: child, Snapshot: snap}); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// splitForkArgs separates flag args from positional args, handling the
// case where positionals appear before flags (cove fork p c -snapshot s).
// Mirrors splitBuildArgs but for the fork-specific flag set: -from and
// -snapshot both take values; no bool flags.
func splitForkArgs(args []string) (flagArgs, posArgs []string, err error) {
	valueFlags := map[string]bool{"from": true, "snapshot": true}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			posArgs = append(posArgs, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			posArgs = append(posArgs, arg)
			continue
		}
		name := strings.TrimLeft(arg, "-")
		if name == "" {
			posArgs = append(posArgs, arg)
			continue
		}
		if before, _, ok := strings.Cut(name, "="); ok {
			name = before
		}
		flagArgs = append(flagArgs, arg)
		if strings.Contains(arg, "=") {
			continue
		}
		if valueFlags[name] {
			if i+1 >= len(args) {
				return nil, nil, fmt.Errorf("flag needs an argument: -%s", name)
			}
			i++
			flagArgs = append(flagArgs, args[i])
		}
	}
	return flagArgs, posArgs, nil
}

// resolveForkInvocation merges the --from ref (if any), -snapshot flag
// (if any), and positional args into the parent/child/snapshot triple
// for ForkVMWithSnapshot. Errors describe the specific shape mismatch
// so users can correct the invocation.
func resolveForkInvocation(fromRef, snapshotFlag string, posArgs []string) (parent, child, snapshot string, err error) {
	if fromRef == "" {
		// Positional form: cove fork <parent> <child>
		if len(posArgs) != 2 {
			return "", "", "", fmt.Errorf("usage: cove fork <parent> <child> [-snapshot <name>]  OR  cove fork --from <parent[@snap]> <child>")
		}
		return posArgs[0], posArgs[1], snapshotFlag, nil
	}
	// --from form: child is the sole positional arg.
	if len(posArgs) != 1 {
		return "", "", "", fmt.Errorf("--from requires exactly one positional <child>; got %d positional args", len(posArgs))
	}
	parent, refSnap, parseErr := parseForkRef(fromRef)
	if parseErr != nil {
		return "", "", "", parseErr
	}
	child = posArgs[0]
	switch {
	case refSnap == "" && snapshotFlag == "":
		snapshot = ""
	case refSnap == "":
		snapshot = snapshotFlag
	case snapshotFlag == "":
		snapshot = refSnap
	case refSnap == snapshotFlag:
		snapshot = refSnap
	default:
		return "", "", "", fmt.Errorf("--from snapshot %q conflicts with -snapshot %q", refSnap, snapshotFlag)
	}
	return parent, child, snapshot, nil
}

// handleTemplate handles the template subcommand.
func handleTemplate(args []string) {
	if len(args) == 0 {
		printTemplateUsage(os.Stderr)
		os.Exit(1)
	}

	subcmd := args[0]
	subargs := args[1:]

	switch subcmd {
	case "help", "-h", "--help":
		printTemplateUsage(os.Stderr)
		return
	case "save":
		if len(subargs) < 1 {
			fmt.Fprintln(os.Stderr, "Usage: cove template save <name>")
			os.Exit(1)
		}
		source := vmconfig.ActiveName()
		if vmName != "" {
			source = vmName
		}
		if err := SaveTemplate(source, subargs[0]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "save-fast":
		if len(subargs) < 1 {
			fmt.Fprintln(os.Stderr, "Usage: cove template save-fast <name>")
			os.Exit(1)
		}
		source := vmconfig.ActiveName()
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
				bytefmt.Size(t.DiskSize),
				mode,
				t.Created.Format("2006-01-02"))
		}
		w.Flush()

	case "create":
		if len(subargs) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: cove template create <template> <name>")
			os.Exit(1)
		}
		if err := CreateFromTemplate(subargs[0], subargs[1]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "delete":
		if len(subargs) < 1 {
			fmt.Fprintln(os.Stderr, "Usage: cove template delete <name>")
			os.Exit(1)
		}
		if err := DeleteTemplate(subargs[0]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Template '%s' deleted.\n", subargs[0])

	default:
		fmt.Fprintf(os.Stderr, "unknown template command: %s\nRun 'cove -help' for usage.\n", subcmd)
		os.Exit(1)
	}
}

// handleVMCommand handles the vm subcommand.
func handleVMCommand(args []string) {
	if len(args) == 0 {
		printVMUsage(os.Stderr)
		os.Exit(1)
	}

	subcmd := args[0]
	subargs := args[1:]

	switch subcmd {
	case "help", "-h", "--help":
		printVMUsage(os.Stderr)
		return
	case "set":
		if len(subargs) < 1 {
			fmt.Fprintln(os.Stderr, "Usage: cove vm set <name>  (use \"\" or 'cove vm unset' to clear)")
			os.Exit(1)
		}
		if subargs[0] == "" {
			if err := vmconfig.UnsetActive(); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			fmt.Println("Active VM cleared.")
			return
		}
		if err := vmconfig.SetActive(subargs[0]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Active VM set to '%s'.\n", subargs[0])

	case "unset":
		if err := vmconfig.UnsetActive(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Active VM cleared.")

	case "tree":
		if len(subargs) > 0 && isHelpArg(subargs[0]) {
			fmt.Println("Usage: cove vm tree [--json] [--orphans] [--reachable-from <image-ref>]")
			fmt.Println()
			fmt.Println("Print VM fork lineage.")
			fmt.Println()
			fmt.Println("Flags:")
			fmt.Println("  --json                       emit a structured forest for scripting")
			fmt.Println("  --orphans                    list only VMs whose parent is missing")
			fmt.Println("  --reachable-from <ref>       show VMs forked from the given image (one hop)")
			return
		}
		treeFS := flag.NewFlagSet("vm tree", flag.ContinueOnError)
		treeFS.SetOutput(os.Stderr)
		treeJSON := treeFS.Bool("json", false, "emit a structured forest for scripting")
		treeOrphans := treeFS.Bool("orphans", false, "list only VMs whose parent is missing")
		treeReachable := treeFS.String("reachable-from", "", "show VMs forked from the given image ref (mutually exclusive with --orphans)")
		if err := treeFS.Parse(subargs); err != nil {
			os.Exit(2)
		}
		if treeFS.NArg() > 0 {
			fmt.Fprintln(os.Stderr, "Usage: cove vm tree [--json] [--orphans] [--reachable-from <image-ref>]")
			os.Exit(1)
		}
		treeOpts := VMTreeOptions{
			JSON:    *treeJSON,
			Orphans: *treeOrphans,
		}
		if *treeReachable != "" {
			if *treeOrphans {
				fmt.Fprintln(os.Stderr, "vm tree: --reachable-from and --orphans are mutually exclusive")
				os.Exit(1)
			}
			ref, err := ParseImageRef(*treeReachable)
			if err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				os.Exit(1)
			}
			treeOpts.ReachableFromImage = &ref
		}
		if err := PrintVMTreeWithOptions(os.Stdout, treeOpts); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "delete":
		delFS := flag.NewFlagSet("vm delete", flag.ContinueOnError)
		delFS.SetOutput(os.Stderr)
		delCascade := delFS.Bool("cascade", false, "recursively delete fork descendants too")
		if err := delFS.Parse(subargs); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return
			}
			os.Exit(2)
		}
		if delFS.NArg() < 1 {
			fmt.Fprintln(os.Stderr, "Usage: cove vm delete [--cascade] <name>")
			os.Exit(1)
		}
		target := delFS.Arg(0)
		prompt := fmt.Sprintf("Delete VM %q? This cannot be undone. [y/N] ", target)
		if *delCascade {
			children, _ := childVMNames(target)
			if len(children) > 0 {
				prompt = fmt.Sprintf("Delete VM %q AND its %d fork descendant(s) (%s)? This cannot be undone. [y/N] ",
					target, len(children), strings.Join(children, ", "))
			}
		}
		ok, err := confirmDeletef("%s", prompt)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if !ok {
			return
		}
		if err := DeleteVMWithOptions(target, DeleteVMOptions{Cascade: *delCascade}); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "rename":
		if len(subargs) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: cove vm rename <old> <new>")
			os.Exit(1)
		}
		if err := RenameVM(subargs[0], subargs[1]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "export":
		if len(subargs) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: cove vm export <name> <path>")
			os.Exit(1)
		}
		if err := ExportVM(subargs[0], subargs[1]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "import":
		if len(subargs) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: cove vm import <path> <name>")
			os.Exit(1)
		}
		if err := ImportVM(subargs[0], subargs[1]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "shared-folder", "shared-folders":
		if sharedFolderCommandBlocked(subargs) {
			fmt.Fprintf(os.Stderr, "error: -sandbox-level %s does not allow shared-folder mutations\n", sandboxLevel)
			os.Exit(1)
		}
		if err := handleVMSharedFolderCommand(subargs); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "config":
		if err := handleVMConfigCommand(subargs); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "unknown vm command: %s\nRun 'cove -help' for usage.\n", subcmd)
		os.Exit(1)
	}
}

// handleSnapshotCommand handles the snapshot subcommand
func handleSnapshotCommand(args []string) {
	if len(args) == 0 {
		printSnapshotUsage(os.Stderr)
		os.Exit(1)
	}

	mgr := snapshotx.NewManager(vmDir)
	subcmd := args[0]
	subargs := args[1:]

	switch subcmd {
	case "help", "-h", "--help":
		printSnapshotUsage(os.Stderr)
		return
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
				bytefmt.Size(s.Size),
				s.Created.Format("2006-01-02 15:04"))
		}
		w.Flush()

	case "delete":
		if len(subargs) < 1 {
			fmt.Fprintln(os.Stderr, "Usage: cove snapshot delete <name>")
			os.Exit(1)
		}
		ok, err := confirmDeletef("Delete snapshot %q? This cannot be undone. [y/N] ", subargs[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if !ok {
			return
		}
		if err := mgr.Delete(subargs[0]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	case "save", "restore":
		if len(subargs) < 1 {
			fmt.Fprintf(os.Stderr, "Usage: cove snapshot %s <name>\n", subcmd)
			os.Exit(1)
		}
		if err := snapshotViaControlSocket(subcmd, subargs[0]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

	default:
		fmt.Fprintf(os.Stderr, "unknown snapshot command: %s\nRun 'cove -help' for usage.\n", subcmd)
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
	case "audit":
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "Usage: cove network audit <run-id>")
			os.Exit(1)
		}
		if err := PrintNetworkAudit(os.Stdout, args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	case "logs":
		logArgs, err := parseNetworkLogsArgs(args[1:])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if err := PrintNetworkLogs(os.Stdout, logArgs.VM, logArgs.Follow); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown network command: %s\nRun 'cove -help' for usage.\n", args[0])
		os.Exit(1)
	}
}
