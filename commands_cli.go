package main

import (
	"encoding/json"
	"fmt"
	"io"

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
	return enc.Encode(covecli.Inventory(commandRegistry))
}

func printCommandsTable(w io.Writer) error {
	return covecli.PrintCommandsTable(w, covecli.Inventory(commandRegistry))
}
