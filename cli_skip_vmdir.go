package main

import "strings"

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
	"helper":  true,
	"daemon":  true,
	"logs":    true,
	"runs":    true,
	"secret":  true,
	"status":  true,
	"version": true,
	"shell":   true,
}

// subcommandSkipsVMDir reports whether the first non-flag argument names a
// command that should bypass vmconfig.EnsureDir at startup.
func subcommandSkipsVMDir(args []string) bool {
	if len(args) == 0 {
		return false
	}
	if args[0] == "vm" && len(args) > 1 && args[1] == "tree" {
		return true
	}
	if args[0] == "run" && argsContainFlag(args[1:], "fork-from") {
		return true
	}
	return vmDirIndependentCommands[args[0]]
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
