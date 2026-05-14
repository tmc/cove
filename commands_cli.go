package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

type commandInfo struct {
	Name              string   `json:"name"`
	Aliases           []string `json:"aliases,omitempty"`
	Summary           string   `json:"summary"`
	Dispatch          string   `json:"dispatch"`
	Outputs           []string `json:"outputs"`
	SafeForDiscovery  bool     `json:"safe_for_discovery"`
	MutatesState      bool     `json:"mutates_state"`
	RequiresRunningVM bool     `json:"requires_running_vm"`
	MayBootVM         bool     `json:"may_boot_vm"`
}

func runCommandsCommand(env commandEnv, _ string, args []string) int {
	if env.Stdout == nil {
		env.Stdout = io.Discard
	}
	if env.Stderr == nil {
		env.Stderr = io.Discard
	}
	if len(args) > 0 && isHelpArg(args[0]) {
		printCommandsUsage(env.Stdout)
		return 0
	}
	jsonOut := false
	for _, arg := range args {
		switch arg {
		case "--json", "-json":
			jsonOut = true
		default:
			err := fmt.Errorf("unknown commands flag %q", arg)
			printCommandsUsage(env.Stderr)
			return commandUsageError(env, err)
		}
	}
	if jsonOut {
		return commandError(env, printCommandsJSON(env.Stdout))
	}
	return commandError(env, printCommandsTable(env.Stdout))
}

func printCommandsUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove commands [--json]

Print the top-level command inventory. --json emits names, aliases, summaries,
dispatch timing, and output-format hints for automation.`)
}

func printCommandsJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(commandInventory())
}

func printCommandsTable(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "COMMAND\tALIASES\tDISPATCH\tOUTPUTS\tSUMMARY"); err != nil {
		return err
	}
	for _, info := range commandInventory() {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", info.Name, strings.Join(info.Aliases, ","), info.Dispatch, strings.Join(info.Outputs, ","), info.Summary); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func commandInventory() []commandInfo {
	out := make([]commandInfo, 0, len(commandRegistry))
	for _, spec := range commandRegistry {
		out = append(out, commandInfo{
			Name:              spec.Name,
			Aliases:           append([]string(nil), spec.Aliases...),
			Summary:           spec.Summary,
			Dispatch:          commandDispatchName(spec.Dispatch),
			Outputs:           commandOutputHints(spec.Name),
			SafeForDiscovery:  commandSafeForDiscovery(spec.Name),
			MutatesState:      commandMutatesState(spec.Name),
			RequiresRunningVM: commandRequiresRunningVM(spec.Name),
			MayBootVM:         commandMayBootVM(spec.Name),
		})
	}
	return out
}

func commandDispatchName(dispatch commandDispatch) string {
	switch dispatch {
	case commandDispatchPreUI:
		return "pre-ui"
	case commandDispatchEarly:
		return "early"
	case commandDispatchLate:
		return "late"
	default:
		return "unknown"
	}
}

func commandSafeForDiscovery(name string) bool {
	return !commandMutatesState(name) && !commandRequiresRunningVM(name) && !commandMayBootVM(name)
}

func commandMutatesState(name string) bool {
	switch name {
	case "action", "agent-sandbox", "agent-upgrade", "bench", "build", "clean", "clone", "compact", "config", "daemon", "disk-detach", "disk-snapshot", "export", "fleet", "fork", "forward", "gc", "helper", "image", "import", "inject", "inject-agent", "install", "network", "pin", "pit", "policy", "provision", "provision-agent", "push", "quota", "rename", "rm", "rosetta", "run", "serve", "shared-folder", "sip", "snapshot", "softreset", "storage", "store", "template", "trace", "uiscript", "unpin", "up", "verify", "vm", "vzscript":
		return true
	default:
		return false
	}
}

func commandRequiresRunningVM(name string) bool {
	switch name {
	case "agent-upgrade", "cp", "ctl", "logs", "shell", "status", "trace", "vzscript":
		return true
	default:
		return false
	}
}

func commandMayBootVM(name string) bool {
	switch name {
	case "agent-sandbox", "build", "install", "run", "up", "action":
		return true
	default:
		return false
	}
}

func commandOutputHints(name string) []string {
	switch name {
	case "action", "commands", "daemon", "diff", "recording", "runner", "runs", "security", "storage", "trace", "vm":
		return []string{"text", "json"}
	case "ctl":
		return []string{"text", "json", "binary"}
	case "serve":
		return []string{"text", "http", "mcp"}
	default:
		return []string{"text"}
	}
}
