package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/tmc/cove/internal/vmconfig"
)

type logsOptions struct {
	VM     string
	Follow bool
	Lines  int
}

const defaultLogLines = 200

func parseLogsArgs(env commandEnv, args []string) (logsOptions, error) {
	env = env.withDefaultIO()
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	follow := fs.Bool("f", false, "follow logs")
	fs.BoolVar(follow, "follow", false, "follow logs")
	lines := fs.Int("n", defaultLogLines, "maximum lines for one-shot logs")
	fs.IntVar(lines, "lines", defaultLogLines, "maximum lines for one-shot logs")
	vmFlag := fs.String("vm", "", "VM name")
	fs.Usage = func() {
		printLogsUsage(fs.Output())
	}
	if err := parseFlagsOrHelp(fs, moveLogsFlagsFirst(args)); err != nil {
		return logsOptions{}, err
	}
	if fs.NArg() > 1 {
		return logsOptions{}, fmt.Errorf("usage: cove logs [-vm name] [vm] [-f] [-n lines]")
	}
	target := strings.TrimSpace(*vmFlag)
	if target == "" {
		target = strings.TrimSpace(vmName)
	}
	if fs.NArg() == 1 {
		positional := fs.Arg(0)
		if target != "" && target != positional {
			return logsOptions{}, fmt.Errorf("logs: -vm %q does not match positional VM %q", target, positional)
		}
		target = positional
	}
	if target == "" {
		return logsOptions{}, fmt.Errorf("usage: cove logs [-vm name] [vm] [-f] [-n lines]")
	}
	if *lines <= 0 {
		return logsOptions{}, fmt.Errorf("logs: -n must be greater than zero")
	}
	return logsOptions{VM: target, Follow: *follow, Lines: *lines}, nil
}

func printLogsUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove logs [-vm name] [vm] [-f] [-n lines]

Show recent guest logs through cove shell. Linux uses journalctl; macOS uses
log show. One-shot output defaults to the most recent 200 lines; use -n or
--lines to change the limit. Use -f or --follow to stream logs without a limit.
If no positional VM is provided, cove uses -vm or the global VM selection.
The -vm, -f, --follow, -n, and --lines flags may appear before or after the VM name.`)
}

func moveLogsFlagsFirst(args []string) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-f", "--follow":
			flags = append(flags, arg)
		case "-vm", "--vm", "-n", "--lines":
			flags = append(flags, arg)
			if i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		default:
			if strings.HasPrefix(arg, "-vm=") || strings.HasPrefix(arg, "--vm=") ||
				strings.HasPrefix(arg, "-n=") || strings.HasPrefix(arg, "--lines=") {
				flags = append(flags, arg)
			} else {
				rest = append(rest, arg)
			}
		}
	}
	return append(flags, rest...)
}

func logsCommand(env commandEnv, args []string) error {
	opts, err := parseLogsArgs(env, args)
	if errors.Is(err, errFlagHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	argv, err := logsGuestCommand(opts)
	if err != nil {
		return err
	}
	shellArgs := append([]string{opts.VM, "--"}, argv...)
	return shellCommand(shellArgs)
}

func logsGuestCommand(opts logsOptions) ([]string, error) {
	dir, err := requireExistingVMForControl(opts.VM)
	if err != nil {
		return nil, err
	}
	lines := opts.Lines
	if lines <= 0 {
		lines = defaultLogLines
	}
	switch vmconfig.DetectOSType(dir) {
	case "Linux":
		if opts.Follow {
			return []string{"journalctl", "-f"}, nil
		}
		return []string{"journalctl", "--since", "1 hour ago", "-n", strconv.Itoa(lines)}, nil
	case "macOS":
		if opts.Follow {
			return []string{"log", "stream"}, nil
		}
		return []string{"/bin/sh", "-lc", "log show --last 1h | tail -n " + strconv.Itoa(lines)}, nil
	default:
		return nil, fmt.Errorf("logs unsupported for %s VM %q", vmconfig.DetectOSType(dir), opts.VM)
	}
}
