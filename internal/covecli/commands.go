package covecli

import (
	"encoding/json"
	"fmt"
	"io"
)

func RunCommandsCommand(stdout, stderr io.Writer, args []string, inventory []Info) int {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	if len(args) > 0 && isHelpArg(args[0]) {
		PrintCommandsUsage(stdout)
		return 0
	}
	jsonOut := false
	for _, arg := range args {
		switch arg {
		case "--json", "-json":
			jsonOut = true
		default:
			PrintCommandsUsage(stderr)
			fmt.Fprintf(stderr, "error: unknown commands flag %q\n", arg)
			return 2
		}
	}
	var err error
	if jsonOut {
		err = PrintCommandsJSON(stdout, inventory)
	} else {
		err = PrintCommandsTable(stdout, inventory)
	}
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func PrintCommandsUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove commands [--json]

Print the top-level command inventory. --json emits names, aliases, summaries,
dispatch timing, and output-format hints for automation.`)
}

func PrintCommandsJSON(w io.Writer, inventory []Info) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(inventory)
}

func isHelpArg(s string) bool {
	switch s {
	case "help", "-h", "-help", "--help":
		return true
	default:
		return false
	}
}
