package main

import (
	"fmt"
	"strings"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

// vmDirIndependentCommands are top-level subcommands that must not create a
// per-VM directory during startup. Some do not operate on VMs; read-only VM
// commands in this list resolve existing VMs inside their own command path.
//
// The motivating case is `cove helper daemon`, which launchd runs as root.
// As root, $HOME is /var/root — on the SIP-sealed system volume — so a
// MkdirAll under it returns EROFS and the daemon never starts.
//
// This is an explicit allowlist rather than a heuristic (e.g. swallow EROFS
// from EnsureDir): for normal commands an unwritable ~/.vz/vms is still a
// real failure that should surface immediately.
var vmDirIndependentCommands = map[string]bool{
	"agent-upgrade":   true,
	"upgrade-agent":   true,
	"commands":        true,
	"config":          true,
	"daemon":          true,
	"helper":          true,
	"image":           true,
	"inject-agent":    true,
	"provision-agent": true,
	"shared-folder":   true,
	"shared-folders":  true,
	"doctor":          true,
	"verify":          true,
	"list":            true,
	"ls":              true,
	"cp":              true,
	"ctl":             true,
	"logs":            true,
	"recording":       true,
	"recordings":      true,
	"rm":              true,
	"remove":          true,
	"destroy":         true,
	"run":             true,
	"runs":            true,
	"secret":          true,
	"security":        true,
	"first-run":       true,
	"status":          true,
	"storage":         true,
	"trace":           true,
	"traces":          true,
	"support":         true,
	"support-bundle":  true,
	"version":         true,
	"shell":           true,
	"vzscript":        true,
}

// subcommandSkipsVMDir reports whether the first non-flag argument names a
// command that should bypass vmconfig.EnsureDir at startup.
func subcommandSkipsVMDir(args []string) bool {
	if len(args) == 0 {
		return false
	}
	if _, ok := lookupCommand(args[0]); !ok && args[0] != "help" {
		return true
	}
	if args[0] == "vm" && len(args) > 1 && args[1] == "tree" {
		return true
	}
	if args[0] == "vm" && len(args) > 1 && args[1] == "delete" {
		return true
	}
	if args[0] == "run" && (vmName != "" || argsContainFlag(args[1:], "vm")) {
		return true
	}
	if args[0] == "run" && argsContainFlag(args[1:], "fork-from") {
		return true
	}
	return vmDirIndependentCommands[args[0]]
}

func requireExistingRunVMDir(name string) (string, error) {
	return requireExistingVMDir("run", name)
}

func requireExistingVMSelection(command, name string) (vmSelection, error) {
	dir, err := requireExistingVMDir(command, name)
	if err != nil {
		return vmSelection{}, err
	}
	return vmSelection{Directory: dir, Name: name}, nil
}

func requireExistingVMDir(command, name string) (string, error) {
	if err := validateNewVMName(name); err != nil {
		return "", fmt.Errorf("%s: invalid VM name %q: %w", command, name, err)
	}
	dir, ok := vmconfig.ExistingPath(name)
	if !ok {
		return "", fmt.Errorf("%s: no VM named %q under %s\n  list VMs: cove list\n  create a VM: cove up -user <name>", command, name, vmconfig.BaseDir())
	}
	if !vmconfig.Validate(dir) {
		return "", fmt.Errorf("%s: VM %q is invalid under %s", command, name, vmconfig.BaseDir())
	}
	return dir, nil
}

func argsContainFlag(args []string, name string) bool {
	short := "-" + name
	long := "--" + name
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == short || arg == long || strings.HasPrefix(arg, short+"=") || strings.HasPrefix(arg, long+"=") {
			return true
		}
		if arg == "--" {
			return false
		}
	}
	return false
}
