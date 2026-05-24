package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/tmc/cove/internal/covecli"
)

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

func commandInventory() []covecli.Info {
	out := make([]covecli.Info, 0, len(commandRegistry))
	for _, spec := range commandRegistry {
		out = append(out, covecli.Info{
			Name:              spec.Name,
			Aliases:           append([]string(nil), spec.Aliases...),
			Summary:           spec.Summary,
			Dispatch:          covecli.DispatchName(spec.Dispatch),
			Outputs:           covecli.OutputHints(spec.Name),
			SafeForDiscovery:  covecli.SafeForDiscovery(spec.Name),
			MutatesState:      covecli.MutatesState(spec.Name),
			RequiresRunningVM: covecli.RequiresRunningVM(spec.Name),
			MayBootVM:         covecli.MayBootVM(spec.Name),
		})
	}
	return out
}
