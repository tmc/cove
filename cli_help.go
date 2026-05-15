package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

func isHelpArg(s string) bool {
	switch s {
	case "help", "-h", "-help", "--help":
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
		if len(subargs) == 1 && (subargs[0] == "--json" || subargs[0] == "-json") {
			if err := printCommandsJSON(os.Stdout); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
				return true, 1
			}
			return true, 0
		}
		if len(subargs) == 0 || isHelpArg(subargs[0]) {
			usage()
			return true, 0
		}
		switch subargs[0] {
		case "advanced":
			usageAdvanced()
		case "first-run":
			printFirstRunUsage(os.Stderr)
		case "commands":
			printCommandsUsage(os.Stderr)
		case "ctl":
			fs, _, _, _, _, _, _ := newCtlFlagSet()
			fs.Usage()
		case "shell":
			printShellUsage(os.Stderr)
		case "up":
			fs, _, _ := newUpFlagSet()
			fs.Usage()
		case "gc":
			printGCUsage(os.Stderr)
		case "compact":
			printCompactUsage(os.Stderr)
		case "build":
			printBuildUsage(os.Stderr)
		case "action":
			printActionUsage(os.Stderr)
		case "runner":
			printRunnerUsage(os.Stderr)
		case "runs":
			printRunsUsage(os.Stderr)
		case "recording", "recordings":
			printRecordingUsage(os.Stderr)
		case "status":
			printStatusUsage(os.Stderr)
		case "trace", "traces":
			printTraceUsage(os.Stderr)
		case "daemon":
			printDaemonUsage(os.Stderr)
		case "cp":
			printCpUsage(os.Stderr)
		case "forward":
			printForwardUsage(os.Stderr)
		case "quota":
			printQuotaUsage(os.Stderr)
		case "diff":
			printDiffUsage(os.Stderr)
		case "image":
			printImageUsage(os.Stderr)
		case "logs":
			printLogsUsage(os.Stderr)
		case "secret":
			printSecretUsage(os.Stderr)
		case "security":
			printSecurityUsage(os.Stderr)
		case "policy":
			printPolicyUsage(os.Stderr)
		case "push":
			printPushUsage(os.Stderr)
		case "pull":
			printPullUsage(os.Stderr)
		case "store":
			printStoreUsage(os.Stderr)
		case "support":
			printSupportUsage(os.Stderr)
		case "support-bundle":
			printSupportBundleUsage(os.Stderr)
		case "provision", "inject":
			fs, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _ := newInjectFlagSet()
			fs.Usage()
		case "provision-agent", "inject-agent":
			if subargs[0] == "inject-agent" {
				printDeprecatedAliasNotice(os.Stderr, "inject-agent", "provision-agent")
			}
			printProvisionAgentUsage(os.Stderr)
		case "doctor", "verify":
			if subargs[0] == "verify" {
				printDeprecatedAliasNotice(os.Stderr, "verify", "doctor")
			}
			fs, _, _, _, _ := newVerifyFlagSet()
			fs.Usage()
		case "template":
			printTemplateUsage(os.Stderr)
		case "vm", "rename", "export", "import":
			if len(subargs) > 1 && subargs[1] == "config" {
				printVMConfigUsage(os.Stderr)
			} else {
				printVMUsage(os.Stderr)
			}
		case "config":
			printVMConfigUsage(os.Stderr)
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
		case "gui":
			printGUIUsage(os.Stderr)
		case "vnc":
			printVNCUsage(os.Stderr)
		case "install":
			printInstallUsage(os.Stderr)
		case "run":
			printRunUsage(os.Stderr)
		case "list", "ls":
			printListUsage(os.Stderr)
		case "clean":
			printCleanUsage(os.Stderr)
		case "clone":
			printCloneUsage(os.Stderr)
		case "fork":
			printForkUsage(os.Stderr)
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
	case "first-run":
		printFirstRunUsage(os.Stdout)
		return true, 0
	case "version":
		if len(subargs) > 0 && isHelpArg(subargs[0]) {
			fmt.Fprintln(os.Stdout, "Usage: cove version")
			fmt.Fprintln(os.Stdout, "\nPrint cove version information (semver, commit, build time).")
			return true, 0
		}
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
		if len(subargs) > 1 && subargs[0] == "ready" && isHelpArg(subargs[1]) {
			printReadyUsage(os.Stderr)
			return true, 0
		}
	case "shell":
		if len(subargs) == 0 || isHelpArg(subargs[0]) {
			printShellUsage(os.Stderr)
			return true, usageExitCode(subargs)
		}
	case "runs":
		if len(subargs) == 0 || isHelpArg(subargs[0]) {
			printRunsUsage(os.Stderr)
			return true, usageExitCode(subargs)
		}
		if len(subargs) > 1 && (subargs[0] == "list" || subargs[0] == "ls") && isHelpArg(subargs[1]) {
			printRunsListUsage(os.Stderr)
			return true, 0
		}
		if len(subargs) > 1 && subargs[0] == "show" && isHelpArg(subargs[1]) {
			printRunsShowUsage(os.Stderr)
			return true, 0
		}
		if len(subargs) > 1 && subargs[0] == "export" && isHelpArg(subargs[1]) {
			printRunsExportUsage(os.Stderr)
			return true, 0
		}
	case "support":
		if len(subargs) == 0 || isHelpArg(subargs[0]) {
			printSupportUsage(os.Stderr)
			return true, usageExitCode(subargs)
		}
		if len(subargs) > 1 && subargs[0] == "bundle" && isHelpArg(subargs[1]) {
			printSupportBundleUsage(os.Stderr)
			return true, 0
		}
	case "support-bundle":
		if len(subargs) > 0 && isHelpArg(subargs[0]) {
			printSupportBundleUsage(os.Stderr)
			return true, 0
		}
	case "commands":
		if len(subargs) == 0 || subargs[0] == "--json" || subargs[0] == "-json" {
			code := runCommandsCommand(newCommandEnv(), cmd, subargs)
			return true, code
		}
		if len(subargs) > 0 && isHelpArg(subargs[0]) {
			printCommandsUsage(os.Stderr)
			return true, 0
		}
	case "action":
		if len(subargs) == 0 || isHelpArg(subargs[0]) {
			printActionUsage(os.Stderr)
			return true, usageExitCode(subargs)
		}
	case "runner":
		if len(subargs) == 0 || isHelpArg(subargs[0]) {
			printRunnerUsage(os.Stderr)
			return true, usageExitCode(subargs)
		}
		if len(subargs) > 1 && subargs[0] == "workflow" && isHelpArg(subargs[1]) {
			printRunnerWorkflowUsage(os.Stderr)
			return true, 0
		}
	case "daemon":
		if len(subargs) == 0 || isHelpArg(subargs[0]) {
			printDaemonUsage(os.Stderr)
			return true, usageExitCode(subargs)
		}
	case "cp":
		if len(subargs) == 0 || isHelpArg(subargs[0]) {
			printCpUsage(os.Stderr)
			return true, usageExitCode(subargs)
		}
	case "forward":
		if len(subargs) == 0 || isHelpArg(subargs[0]) {
			printForwardUsage(os.Stderr)
			return true, usageExitCode(subargs)
		}
	case "quota":
		if len(subargs) == 0 || isHelpArg(subargs[0]) {
			printQuotaUsage(os.Stderr)
			return true, usageExitCode(subargs)
		}
	case "diff":
		if len(subargs) == 0 || isHelpArg(subargs[0]) {
			printDiffUsage(os.Stderr)
			return true, usageExitCode(subargs)
		}
	case "image":
		if len(subargs) == 0 || isHelpArg(subargs[0]) {
			printImageUsage(os.Stderr)
			return true, usageExitCode(subargs)
		}
	case "logs":
		if len(subargs) == 0 && strings.TrimSpace(vmName) != "" {
			return false, 0
		}
		if len(subargs) == 0 || isHelpArg(subargs[0]) {
			printLogsUsage(os.Stderr)
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
	case "push":
		if len(subargs) > 0 && isHelpArg(subargs[0]) {
			printPushUsage(os.Stderr)
			return true, 0
		}
	case "pull":
		if len(subargs) > 0 && isHelpArg(subargs[0]) {
			printPullUsage(os.Stderr)
			return true, 0
		}
	case "policy":
		if len(subargs) == 0 || isHelpArg(subargs[0]) {
			printPolicyUsage(os.Stderr)
			return true, usageExitCode(subargs)
		}
	case "security":
		if len(subargs) == 0 || isHelpArg(subargs[0]) {
			printSecurityUsage(os.Stderr)
			return true, usageExitCode(subargs)
		}
	case "recording", "recordings":
		if len(subargs) == 0 || isHelpArg(subargs[0]) {
			printRecordingUsage(os.Stderr)
			return true, usageExitCode(subargs)
		}
		if len(subargs) > 1 && (subargs[0] == "list" || subargs[0] == "ls") && isHelpArg(subargs[1]) {
			printRecordingListUsage(os.Stderr)
			return true, 0
		}
		if len(subargs) > 1 && subargs[0] == "export" && isHelpArg(subargs[1]) {
			printRecordingExportUsage(os.Stderr)
			return true, 0
		}
	case "status":
		if len(subargs) > 0 && isHelpArg(subargs[0]) {
			printStatusUsage(os.Stderr)
			return true, 0
		}
	case "trace", "traces":
		if len(subargs) == 0 || isHelpArg(subargs[0]) {
			printTraceUsage(os.Stderr)
			return true, usageExitCode(subargs)
		}
		if len(subargs) > 1 && subargs[0] == "status" && isHelpArg(subargs[1]) {
			printTraceStatusUsage(os.Stderr)
			return true, 0
		}
		if len(subargs) > 1 && subargs[0] == "capabilities" && isHelpArg(subargs[1]) {
			printTraceCapabilitiesUsage(os.Stderr)
			return true, 0
		}
	case "store":
		if len(subargs) == 0 || isHelpArg(subargs[0]) {
			printStoreUsage(os.Stderr)
			return true, usageExitCode(subargs)
		}
		if subargs[0] == "gc" && len(subargs) > 1 && isHelpArg(subargs[1]) {
			printStoreGCUsage(os.Stderr)
			return true, 0
		}
	case "provision", "inject":
		if len(subargs) > 0 && isHelpArg(subargs[0]) {
			fs, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _ := newInjectFlagSet()
			fs.Usage()
			return true, 0
		}
	case "provision-agent", "inject-agent":
		if len(subargs) > 0 && isHelpArg(subargs[0]) {
			if cmd == "inject-agent" {
				printDeprecatedAliasNotice(os.Stderr, "inject-agent", "provision-agent")
			}
			printProvisionAgentUsage(os.Stderr)
			return true, 0
		}
	case "doctor", "verify":
		if len(subargs) > 0 && isHelpArg(subargs[0]) {
			if cmd == "verify" {
				printDeprecatedAliasNotice(os.Stderr, "verify", "doctor")
			}
			fs, _, _, _, _ := newVerifyFlagSet()
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
	case "gui":
		if len(subargs) == 0 || isHelpArg(subargs[0]) {
			printGUIUsage(os.Stderr)
			return true, usageExitCode(subargs)
		}
	case "vnc":
		if len(subargs) == 0 || isHelpArg(subargs[0]) {
			printVNCUsage(os.Stderr)
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
	case "list", "ls":
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
	case "fork":
		if len(subargs) > 0 && isHelpArg(subargs[0]) {
			printForkUsage(os.Stderr)
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

// printDeprecatedAliasNotice prints a one-line note when a command is invoked
// under a deprecated alias, so the alias name appears in -h output.
func printDeprecatedAliasNotice(w io.Writer, alias, canonical string) {
	fmt.Fprintf(w, "Note: %q is a deprecated alias for %q.\n\n", alias, canonical)
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

Delete disposable VM clones and inactive ephemeral forks.

Options:
  -dry-run              Print disposable clones and ephemeral forks without deleting them
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
  delete [--cascade] <name>
                          Delete a VM. Refuses if the VM has fork
                          descendants unless --cascade is set; with
                          --cascade, descendants are deleted first.
  rename <old> <new>      Rename a VM
  export <name> <path>    Export a VM to a tarball
  import <path> <name>    Import a VM from a tarball
  tree [--json] [--orphans] [--reachable-from <image-ref>]
                          Print fork lineage. --json emits structured
                          output; --orphans lists only VMs whose
                          parent is missing; --reachable-from <ref>
                          shows VMs forked from the given image as a
                          one-hop tree (mutually exclusive with
                          --orphans).
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
  list                              List configured folders
  status [mount-point]              Show mount status in guest
  pending [vm]                      List saved folders not mounted now
  add <host-path> [tag] [ro|rw]     Save and live-apply when running
  remove <tag-or-path>              Remove a saved folder
  clear                             Remove all saved folders
  mount [mount-point]               Retry guest mount via agent`)
}

func printInstallUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove install [flags]

Install a new VM. macOS by default; pass -linux for Linux.

Common flags:
  -ipsw <path>            macOS restore image (downloaded if omitted)
  -linux                  install Linux instead of macOS
  -distro <name>          Linux distro: ubuntu, debian, fedora, alpine, nixos
  -nixos                  install NixOS (implies -linux -distro nixos)
  -desktop                with -linux, install Ubuntu Desktop
  -nested                 with -linux, enable nested virtualization on supported hosts
  -iso <path>             use a local ISO instead of auto-download
  -cpu N                  CPU count (default 2)
  -memory N               memory in GB (default 4)
  -disk-size N            disk size in GB (default 64)
  -force                  overwrite an existing VM disk
  -provision-user <name>  create user during install
  -provision-password <p> password for provisioned user (prompts if empty)
  -vzscripts a,b,c        run vzscript recipes after install
  -vm <name>              target VM name (default: active VM)

Examples:
  cove install -ipsw ~/.cache/vz/restore.ipsw
  cove install -linux -distro alpine
  cove install -nixos
  cove install -linux -provision-user me`)
}

func printGUIUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove run -gui [flags]
       cove ctl -vm <name> gui status|open|close

Show or control the native VM display window.

Examples:
  cove run -gui
  cove ctl -vm work gui status
  cove ctl -vm work gui open`)
}

func printVNCUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove run -vnc :5901 [flags]
       cove ctl -vm <name> vnc status

Expose a private VNC server for a running VM.

Common flags:
  -vnc-password <password>   require a VNC password
  -vnc-bonjour <name>        advertise VNC with Bonjour

Examples:
  cove run -vnc :5901 -vnc-password <password>
  cove ctl -vm work vnc status`)
}

func printRunUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove run [flags]

Boot the selected VM (resumes from suspend state if present).

Common flags:
  -gui / -headless        show or hide the VM display window
  -linux                  run a Linux VM
  -nested                 with -linux, enable nested virtualization on supported hosts
  -recovery               boot macOS into recovery mode
  -no-resume / -cold-boot discard saved suspend state and cold boot
  -network / --net <mode> nat (default), bridged:<iface>, host-only, none
  --pf host:guest         forward host TCP to guest vsock (repeatable)
  -vol /host[:tag][:ro]   mount a host directory (repeatable)
  -usb /path/disk.img[:ro] attach a USB mass-storage device (repeatable)
  -display WxH[@PPI]      set display resolution (repeatable)
  -http <addr>            expose per-VM HTTP API (e.g. :7777)
  -vnc <port>             start private VNC server (e.g. :5901)
  -gdb <port>             start private GDB debug stub (e.g. :1234)
  -host-containment       fail closed for host-escape features
  -unattended             fully unattended setup (disk + OCR fallback)
  -boot-commands <file>   custom boot automation vzscript
  -vm <name>              target VM (default: active VM)

Ephemeral fork:
  -fork-from <image-ref>  boot a short-lived VM from a local image ref;
                          auto-deleted on exit with -ephemeral. VM-parent
                          RAM-overlay forks are not implemented; use
                          cove fork or cove clone --linked for VM parents.
  -fork-name <name>       explicit name for the forked VM
  -keep                   with -fork-from, retain the forked vmDir
                          after exit so logs / control.sock persist
  -ephemeral              with -fork-from <image-ref>, destroy the
                          materialized child on stop

Examples:
  cove run -gui
  cove run -linux -gui -vol ~/code:code
  cove run -fork-from macos-runner:latest -ephemeral -headless
  cove fork base-vm scratch-1 && cove run -vm scratch-1 -gui`)
}

func printListUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove list
       cove ls

List installed VMs (with state and size), templates, and any orphan
directories under ~/.vz/vms that no longer contain a valid disk image.

The active VM is marked with '*' in the ACTIVE column. Orphans can
be removed with: cove vm delete <name>

Example:
  cove ls`)
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

func printForkUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove fork <parent> <child> [-snapshot <name>]
       cove fork --from <parent[@snapshot]> <child> [-snapshot <name>]

Create a child VM as an APFS copy-on-write fork of <parent>. The child
shares disk blocks with the parent until either side writes, and gets a
fresh machine identity and MAC address.

This is "cove clone --linked" with explicit lineage and forced fresh
identity — see docs/designs/013-vm-fork.md for the model.

Plain fork (no -snapshot, no @snap):
  Permitted while parent is running, but best-effort: APFS clonefile
  snapshots parent's disk at clone time, and subsequent parent writes
  during the call may produce inconsistent state in rare cases. Use
  -snapshot for guaranteed-consistent forks.

Snapshot-seeded fork (-snapshot <name> or --from parent@<name>):
  Seeds the child's suspend.vmstate from parent/snapshots/<name>.vmstate
  and attempts a VZ state restore on first boot. Requires:

    - parent VM stopped (the fork acquires parent's run.lock exclusively
      for the duration of the copy)
    - parent has a saved snapshot — create one first while the parent
      is running with: cove snapshot save <name>

  Current behavior (Phase 2): the child's machine.id is rotated like
  any fork, so VZ rejects the seeded state and the existing
  suspend-restore fallback in macos.go moves the seed aside and
  cold-boots from the cloned disk. The seeded state is best-effort
  today; reaching instant-resume requires a future identity-preserving
  fork option that copies the parent's machine.id alongside the
  vmstate. See docs/designs/013-vm-fork.md and the Phase 2 bench notes
  for details.

Examples:
  cove fork macos-base scratch-1
  cove fork macos-base scratch-1 -snapshot clean
  cove fork --from macos-base@clean scratch-1`)
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
  restore <name> [-system]              Fork the live disk from snapshot (CoW; snapshot preserved)
  list                                  List all disk snapshots
  delete <name>                         Delete a disk snapshot

Examples:
  cove disk-snapshot save checkpoint1
  cove disk-snapshot run checkpoint1 -ram
  cove disk-snapshot list

Note: VM should be stopped for consistent disk snapshots.`)
}
