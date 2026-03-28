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
		case "inject", "provision":
			fs, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _, _ := newInjectFlagSet()
			fs.Usage()
		case "doctor", "verify":
			fs, _, _ := newVerifyFlagSet()
			fs.Usage()
		case "template":
			printTemplateUsage(os.Stderr)
		case "vm":
			printVMUsage(os.Stderr)
		case "snapshot":
			printSnapshotUsage(os.Stderr)
		case "shared-folder", "shared-folders":
			printSharedFolderUsage(os.Stderr)
		case "vzscript":
			printVzscriptUsage(os.Stderr)
		case "network":
			fmt.Println(NetworkModeHelp())
		case "rosetta":
			fmt.Println(RosettaHelp())
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
	case "inject", "provision":
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
	}

	return false, 0
}

func printTemplateUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: vz-macos template <command>

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
	fmt.Fprintln(w, `Usage: vz-macos vm <command>

Commands:
  set <name>              Set the active VM
  delete <name>           Delete a VM
  rename <old> <new>      Rename a VM
  export <name> <path>    Export a VM to a tarball
  import <path> <name>    Import a VM from a tarball
  shared-folder ...       Manage shared folders (alias: vz-macos shared-folder ...)`)
}

func printSnapshotUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: vz-macos snapshot <command>

Commands:
  list                    List available snapshots
  save <name>             Save current VM state (VM must be running)
  restore <name>          Restore a snapshot into the running VM
  delete <name>           Delete a snapshot

Snapshots are stored under ~/.vz/vms/<vm>/snapshots/.`)
}

func printSharedFolderUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: vz-macos shared-folder <command>

Alias:
  vz-macos vm shared-folder <command>

Commands:
  list
  status [mount-point]
  add <host-path> [tag] [ro|rw]
  remove <tag-or-path>
  clear
  mount [mount-point]`)
}
