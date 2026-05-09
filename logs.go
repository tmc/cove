package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

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
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: cove logs <vm> [-f]")
		fs.PrintDefaults()
	}
	if err := parseFlagsOrHelp(fs, moveLogsFlagsFirst(args)); err != nil {
		return logsOptions{}, err
	}
	if fs.NArg() != 1 {
		return logsOptions{}, fmt.Errorf("usage: cove logs <vm> [-f]")
	}
	return logsOptions{VM: fs.Arg(0), Follow: *follow}, nil
}

func moveLogsFlagsFirst(args []string) []string {
	var flags, rest []string
	for _, arg := range args {
		switch arg {
		case "-f", "--follow":
			flags = append(flags, arg)
		default:
			rest = append(rest, arg)
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
