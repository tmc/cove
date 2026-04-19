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
		case "provision", "inject":
			fs, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _ := newInjectFlagSet()
			fs.Usage()
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
		default:
			fmt.Fprintf(os.Stderr, "unknown help topic: %s\n", subargs[0])
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
	case "provision", "inject":
		if len(subargs) > 0 && isHelpArg(subargs[0]) {
			fs, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _ := newInjectFlagSet()
			fs.Usage()
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
	}

	return false, 0
}

func printGCUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove gc [options]

Delete disposable VM clones created with -disposable.

Options:
  -dry-run              Print disposable clones without deleting them
  -older-than duration  Only delete disposable clones older than the given age`)
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
  set <name>              Set the active VM
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
