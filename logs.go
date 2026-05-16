package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

type logsOptions struct {
	VM     string
	Follow bool
}

func parseLogsArgs(args []string) (logsOptions, error) {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	follow := fs.Bool("f", false, "follow logs")
	fs.BoolVar(follow, "follow", false, "follow logs")
	vmFlag := fs.String("vm", "", "VM name")
	fs.Usage = func() {
		printLogsUsage(fs.Output())
	}
	if err := parseFlagsOrHelp(fs, moveLogsFlagsFirst(args)); err != nil {
		return logsOptions{}, err
	}
	if fs.NArg() > 1 {
		return logsOptions{}, fmt.Errorf("usage: cove logs [-vm name] [vm] [-f]")
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
		return logsOptions{}, fmt.Errorf("usage: cove logs [-vm name] [vm] [-f]")
	}
	return logsOptions{VM: target, Follow: *follow}, nil
}

func printLogsUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove logs [-vm name] [vm] [-f]

Show recent guest logs through cove shell. Linux uses journalctl; macOS uses
log show. Use -f or --follow to stream logs.
If no positional VM is provided, cove uses -vm or the global VM selection.`)
}

func moveLogsFlagsFirst(args []string) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "-f", "--follow":
			flags = append(flags, arg)
		case "-vm", "--vm":
			flags = append(flags, arg)
			if i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		default:
			if strings.HasPrefix(arg, "-vm=") || strings.HasPrefix(arg, "--vm=") {
				flags = append(flags, arg)
			} else {
				rest = append(rest, arg)
			}
		}
	}
	return append(flags, rest...)
}

func logsCommand(args []string) error {
	opts, err := parseLogsArgs(args)
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
	dir, ok := vmconfig.ExistingPath(opts.VM)
	if !ok {
		return nil, fmt.Errorf("no VM named %q under %s", opts.VM, vmconfig.BaseDir())
	}
	switch vmconfig.DetectOSType(dir) {
	case "Linux":
		if opts.Follow {
			return []string{"journalctl", "-f"}, nil
		}
		return []string{"journalctl", "--since", "1 hour ago"}, nil
	case "macOS":
		if opts.Follow {
			return []string{"log", "stream"}, nil
		}
		return []string{"log", "show", "--last", "1h"}, nil
	default:
		return nil, fmt.Errorf("logs unsupported for %s VM %q", vmconfig.DetectOSType(dir), opts.VM)
	}
}
