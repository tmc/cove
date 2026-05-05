package main

// vmDirIndependentCommands are top-level subcommands that do not operate on a
// per-VM directory and therefore must not require ~/.vz/vms to be writable.
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
	"runs":    true,
	"secret":  true,
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
	return vmDirIndependentCommands[args[0]]
}
