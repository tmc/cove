package main

import (
	"fmt"
	"io"
	"os"
)

func isHelpArg(s string) bool {
	switch s {
	case "help", "-h", "--help":
		return true
	default:
		return false
	}
}

func usageExitCode(args []string) int {
	if len(args) > 0 && isHelpArg(args[0]) {
		return 0
	}
	return 2
}

func handleEarlyCLI(args []string) (handled bool, exitCode int) {
	if len(args) == 0 {
		return false, 0
	}

	cmd := args[0]
	subargs := args[1:]

	switch cmd {
	case "help":
		if len(subargs) == 0 {
			usage()
			return true, 0
		}
		switch subargs[0] {
		case "ctl":
			fs, _, _, _, _, _, _ := newCtlFlagSet()
			fs.Usage()
		case "up":
			fs, _, _ := newUpFlagSet()
			fs.Usage()
		case "gc":
			printGCUsage(os.Stderr)
		case "compact":
			printCompactUsage(os.Stderr)
		case "provision", "inject":
			fs, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _ := newInjectFlagSet()
			fs.Usage()
		case "provision-agent", "inject-agent":
			printProvisionAgentUsage(os.Stderr)
		case "doctor", "verify":
			fs, _, _ := newVerifyFlagSet()
			fs.Usage()
		case "template":
			printTemplateUsage(os.Stderr)
		case "vm":
			if len(subargs) > 1 && subargs[1] == "config" {
				printVMConfigUsage(os.Stderr)
			} else {
				printVMUsage(os.Stderr)
			}
		case "snapshot":
			printSnapshotUsage(os.Stderr)
		case "pit":
			printPITUsageHelp(os.Stderr)
		case "shared-folder", "shared-folders":
			printSharedFolderUsage(os.Stderr)
		case "vzscript":
			printVzscriptUsage(os.Stderr)
		case "serve":
			printServeUsage()
		case "network":
			fmt.Println(NetworkModeHelp())
		case "proxy":
			printProxyUsage(os.Stderr)
		case "rosetta":
			fmt.Println(RosettaHelp())
		case "helper":
			_ = helperUsage()
		case "install":
			printInstallUsage(os.Stderr)
		case "run":
			printRunUsage(os.Stderr)
		case "list":
			printListUsage(os.Stderr)
		case "clean":
			printCleanUsage(os.Stderr)
		case "clone":
			printCloneUsage(os.Stderr)
		case "agent-upgrade", "upgrade-agent":
			printAgentUpgradeUsage(os.Stderr)
		case "disk-detach":
			printDiskDetachUsage(os.Stderr)
		case "disk-snapshot":
			printDiskSnapshotUsageHelp(os.Stderr)
		default:
			fmt.Fprintf(os.Stderr, "unknown help topic: %s\n\n", subargs[0])
			usage()
			return true, 2
		}
		return true, 0
	case "version":
		fmt.Println(versionInfo())
		return true, 0
	case "network":
		if len(subargs) == 0 || isHelpArg(subargs[0]) {
			fmt.Println(NetworkModeHelp())
			return true, 0
		}
	case "proxy":
		if len(subargs) == 0 || isHelpArg(subargs[0]) {
			printProxyUsage(os.Stderr)
			return true, 0
		}
	case "rosetta":
		if len(subargs) == 0 || isHelpArg(subargs[0]) {
			fmt.Println(RosettaHelp())
			return true, 0
		}
	case "ctl":
		if len(subargs) == 0 || isHelpArg(subargs[0]) {
			fs, _, _, _, _, _, _ := newCtlFlagSet()
			fs.Usage()
			return true, usageExitCode(subargs)
		}
	case "up":
		if len(subargs) > 0 && isHelpArg(subargs[0]) {
			fs, _, _ := newUpFlagSet()
			fs.Usage()
			return true, 0
		}
	case "gc":
		if len(subargs) == 0 || isHelpArg(subargs[0]) {
			printGCUsage(os.Stderr)
			return true, usageExitCode(subargs)
		}
	case "compact":
		if len(subargs) > 0 && isHelpArg(subargs[0]) {
			printCompactUsage(os.Stderr)
			return true, 0
		}
	case "provision", "inject":
		if len(subargs) > 0 && isHelpArg(subargs[0]) {
			fs, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _ := newInjectFlagSet()
			fs.Usage()
			return true, 0
		}
	case "provision-agent", "inject-agent":
		if len(subargs) > 0 && isHelpArg(subargs[0]) {
			printProvisionAgentUsage(os.Stderr)
			return true, 0
		}
	case "doctor", "verify":
		if len(subargs) > 0 && isHelpArg(subargs[0]) {
			fs, _, _ := newVerifyFlagSet()
			fs.Usage()
			return true, 0
		}
	case "template":
		if len(subargs) == 0 || isHelpArg(subargs[0]) {
			printTemplateUsage(os.Stderr)
			return true, usageExitCode(subargs)
		}
	case "vm":
		if len(subargs) == 0 || isHelpArg(subargs[0]) {
			printVMUsage(os.Stderr)
			return true, usageExitCode(subargs)
		}
		if len(subargs) > 0 && subargs[0] == "config" && (len(subargs) == 1 || isHelpArg(subargs[1])) {
			printVMConfigUsage(os.Stderr)
			return true, usageExitCode(subargs[1:])
		}
	case "snapshot":
		if len(subargs) == 0 || isHelpArg(subargs[0]) {
			printSnapshotUsage(os.Stderr)
			return true, usageExitCode(subargs)
		}
	case "pit":
		if len(subargs) == 0 || isHelpArg(subargs[0]) {
			printPITUsageHelp(os.Stderr)
			return true, usageExitCode(subargs)
		}
	case "shared-folder", "shared-folders":
		if len(subargs) == 0 || isHelpArg(subargs[0]) {
			printSharedFolderUsage(os.Stderr)
			return true, usageExitCode(subargs)
		}
	case "vzscript":
		if len(subargs) == 0 || isHelpArg(subargs[0]) {
			printVzscriptUsage(os.Stderr)
			return true, usageExitCode(subargs)
		}
	case "serve":
		if len(subargs) > 0 && isHelpArg(subargs[0]) {
			printServeUsage()
			return true, 0
		}
	case "helper":
		if len(subargs) == 0 || isHelpArg(subargs[0]) {
			_ = helperUsage()
			return true, usageExitCode(subargs)
		}
	case "install":
		if len(subargs) > 0 && isHelpArg(subargs[0]) {
			printInstallUsage(os.Stderr)
			return true, 0
		}
	case "run":
		if len(subargs) > 0 && isHelpArg(subargs[0]) {
			printRunUsage(os.Stderr)
			return true, 0
		}
	case "list":
		if len(subargs) > 0 && isHelpArg(subargs[0]) {
			printListUsage(os.Stderr)
			return true, 0
		}
	case "clean":
		if len(subargs) > 0 && isHelpArg(subargs[0]) {
			printCleanUsage(os.Stderr)
			return true, 0
		}
	case "clone":
		if len(subargs) > 0 && isHelpArg(subargs[0]) {
			printCloneUsage(os.Stderr)
			return true, 0
		}
	case "agent-upgrade", "upgrade-agent":
		if len(subargs) > 0 && isHelpArg(subargs[0]) {
			printAgentUpgradeUsage(os.Stderr)
			return true, 0
		}
	case "disk-detach":
		if len(subargs) > 0 && isHelpArg(subargs[0]) {
			printDiskDetachUsage(os.Stderr)
			return true, 0
		}
	case "disk-snapshot":
		if len(subargs) > 0 && isHelpArg(subargs[0]) {
			printDiskSnapshotUsageHelp(os.Stderr)
			return true, 0
		}
	}

	return false, 0
}

func printProvisionAgentUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove provision-agent

Provision the vz-agent daemon into the selected VM. If the VM is running
and reachable on its control socket, the agent is pushed over vsock and
restarted in place. Otherwise, the disk is mounted offline and the agent
is written to /usr/local/bin and /Library/LaunchDaemons.

Idempotent: if the guest agent version already matches the host, the
command returns without rebuilding.

Use -vm <name> (before the subcommand) to target a specific VM.`)
}

func printGCUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove gc [options]

Delete disposable VM clones created with -disposable.

Options:
  -dry-run              Print disposable clones without deleting them
  -older-than duration  Only delete disposable clones older than the given age`)
}

func printCompactUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove compact [options] [vm]

Zero free space inside a running guest so later OCI pushes upload less data.

Options:
  -vm name   Target VM name (default: active VM)

Requires a running VM with the root vz-agent reachable on its control socket.`)
}

func printProxyUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove run -proxy http://HOST:PORT

Configure guest system HTTP/HTTPS proxy settings after boot.

Behavior:
  - Linux writes /etc/environment.d/99-cove-proxy.conf and /etc/profile.d/99-cove-proxy.sh
  - macOS configures proxy settings with networksetup through the guest user agent
  - clean shutdown restores the previous guest proxy state best-effort

Preflight:
  - -sandbox-level strict rejects -proxy
  - -network none rejects -proxy
  - macOS -runtime-profile minimal rejects -proxy
  - Linux -no-agent only affects install/provisioning; existing VM runs use durable guest-agent state from config.json

Troubleshooting:
  - If preflight says the Linux guest lacks vz-agent, run: cove provision-agent
  - If macOS runtime probing cannot reach the user agent, log in graphically and retry
  - If .proxy-state.json remains in the VM directory, run doctor or boot again with the same -proxy and stop cleanly`)
}

func printTemplateUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove template <command>

Commands:
  save <name>                Save the active VM as a compressed template
  save-fast <name>           Save the active VM as a fast APFS clone template
  list                       List available templates
  create <template> <name>   Create a VM from a template
  delete <name>              Delete a template

Fast templates use APFS clonefile for instant copy-on-write creation.
Compressed templates take longer to save but use less disk space.`)
}

func printVMUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove vm <command>

Commands:
  set <name>              Set the active VM (pass "" to clear)
  unset                   Clear the active-VM marker
  delete <name>           Delete a VM
  rename <old> <new>      Rename a VM
  export <name> <path>    Export a VM to a tarball
  import <path> <name>    Import a VM from a tarball
  config <command>        Export/import a framework config snapshot
  shared-folder ...       Manage shared folders (alias: cove shared-folder ...)`)
}

func printVMConfigUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove vm config <command>

Commands:
  export <path>           Write the current framework config snapshot
  import <path>           Decode a snapshot, print a summary, and store raw bytes

Export uses the currently selected VM directory and the active VM settings
already persisted on disk. Import does not mutate identity files; it stores the
raw config snapshot in the VM directory for later inspection.`)
}

func printSnapshotUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove snapshot <command>

Commands:
  list                    List available snapshots
  save <name>             Save current VM state (VM must be running)
  restore <name>          Restore a snapshot into the running VM
  delete <name>           Delete a snapshot

Snapshots are stored under ~/.vz/vms/<vm>/snapshots/.`)
}

func printPITUsageHelp(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove pit <command>

Experimental point-in-time save and recovery.

Commands:
  save <name>             Save a coordinated VM-state + disk + config snapshot
  list                    List saved PIT snapshots
  restore <name>          Stage a PIT snapshot for resume on the next run
  run <name> [-ram]       Boot a disposable clone from the PIT disk
  swap <name> [-ram]      Live-swap disk 0 to the PIT disk
  delete <name>           Delete a PIT snapshot

The save path requires a running VM with the control socket active.
Restore writes disk.img and suspend.vmstate in the selected VM directory.`)
}

func printSharedFolderUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove shared-folder <command>

Alias:
  cove vm shared-folder <command>

Commands:
  list
  status [mount-point]
  add <host-path> [tag] [ro|rw]
  remove <tag-or-path>
  clear
  mount [mount-point]`)
}

func printInstallUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove install [flags]

Install a new VM. macOS by default; pass -linux for Ubuntu.

Common flags:
  -ipsw <path>            macOS restore image (downloaded if omitted)
  -linux                  install Ubuntu Server ARM64 instead of macOS
  -desktop                with -linux, install Ubuntu Desktop
  -iso <path>             use a local ISO instead of auto-download
  -cpu N                  CPU count (default 2)
  -memory N               memory in GB (default 4)
  -disk-size N            disk size in GB (default 64)
  -force                  overwrite an existing VM disk
  -provision-user <name>  create user during install
  -provision-password <p> password for provisioned user
  -vzscripts a,b,c        run vzscript recipes after install
  -vm <name>              target VM name (default: active VM)

Examples:
  cove install -ipsw ~/.cache/vz/restore.ipsw
  cove install -linux -provision-user me`)
}

func printRunUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove run [flags]

Boot the selected VM (resumes from suspend state if present).

Common flags:
  -gui / -headless        show or hide the VM display window
  -linux                  run a Linux VM
  -recovery               boot macOS into recovery mode
  -no-resume / -cold-boot discard saved suspend state and cold boot
  -network <mode>         nat (default), bridged:<iface>, vmnet, filehandle, none
  -vol /host[:tag][:ro]   mount a host directory (repeatable)
  -usb /path/disk.img[:ro] attach a USB mass-storage device (repeatable)
  -display WxH[@PPI]      set display resolution (repeatable)
  -http <addr>            expose per-VM HTTP API (e.g. :7777)
  -unattended             fully unattended setup (disk + OCR fallback)
  -boot-commands <file>   custom boot automation vzscript
  -vm <name>              target VM (default: active VM)

Examples:
  cove run -gui
  cove run -linux -gui -vol ~/code:code`)
}

func printListUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove list

List installed VMs (with state and size), templates, and any orphan
directories under ~/.vz/vms that no longer contain a valid disk image.

The active VM is marked with '*' in the ACTIVE column. Orphans can
be removed with: cove vm delete <name>

Example:
  cove list`)
}

func printCleanUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove clean [-vm <name>]

Remove the per-VM artifacts (disk.img, aux.img, hw.model, machine.id,
boot-args.txt, .inject-succeeded) and the provisioning staging
directory from the selected VM. The VM directory itself is kept.

Use 'cove vm delete <name>' to remove the entire VM directory.

Examples:
  cove clean
  cove -vm test-vm clean`)
}

func printCloneUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove clone [source] <target> [--linked] [--with-agent]

Clone a VM. If [source] is omitted, the active VM is cloned.

Flags:
  --linked                use APFS copy-on-write (instant, share blocks)
  --with-agent            provision vz-agent into the new clone

Examples:
  cove clone work-vm
  cove clone macos-base scratch --linked --with-agent`)
}

func printAgentUpgradeUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove agent-upgrade [-vm <name>]

Build a fresh vz-agent binary from the current source tree, copy it to
the running VM via the control socket, restart the LaunchDaemon, and
verify the new version is reachable.

The target VM must be running and have an existing vz-agent
installation (use 'cove provision-agent' for offline install).

Aliases: upgrade-agent

Example:
  cove agent-upgrade`)
}

func printDiskDetachUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove disk-detach [-vm <name>]

Detach the VM's disk image if it is still attached on the host (e.g.
left over from a crashed provision or verify run). Safe to run when
the disk is not attached — it is a no-op in that case.

Example:
  cove disk-detach`)
}

func printDiskSnapshotUsageHelp(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove disk-snapshot <command>

Disk-level snapshots using APFS clonefile (copy-on-write).
Unlike VM state snapshots, these snapshot the actual disk contents.

Commands:
  save <name> [-system] [-desc "..."]   Save disk snapshot
  run <name> [-ram]                     Boot a disposable clone from snapshot
  restore <name> [-system]              Restore disks from snapshot
  list                                  List all disk snapshots
  delete <name>                         Delete a disk snapshot

Examples:
  cove disk-snapshot save checkpoint1
  cove disk-snapshot run checkpoint1 -ram
  cove disk-snapshot list

Note: VM should be stopped for consistent disk snapshots.`)
}
