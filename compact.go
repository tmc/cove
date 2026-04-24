package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	agentstate "github.com/tmc/vz-macos/internal/agent"
	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

const compactTimeout = 30 * time.Minute

type compactClient interface {
	AgentPingTyped() (string, error)
	AgentExecTypedTimeout(args []string, env map[string]string, workDir string, timeout time.Duration) (*controlpb.AgentExecResponse, error)
}

type compactResult struct {
	Platform string
	Stdout   string
	Stderr   string
}

func handleCompact(args []string) error {
	fs, target := newCompactFlagSet(os.Stderr)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	pos := fs.Args()
	if len(pos) > 1 {
		return fmt.Errorf("usage: cove compact [options] [vm]")
	}
	if len(pos) == 1 {
		if *target != "" {
			return fmt.Errorf("usage: cove compact [options] [vm]")
		}
		*target = pos[0]
	}

	vmDirectory := vmDir
	if *target != "" {
		vmDirectory = GetVMPath(*target)
	}
	if !ValidateVM(vmDirectory) {
		return fmt.Errorf("vm not found or invalid: %s", vmDirectory)
	}

	result, err := compactVM(vmDirectory)
	if err != nil {
		return err
	}
	printCompactResult(os.Stdout, result)
	return nil
}

func newCompactFlagSet(w io.Writer) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet("compact", flag.ContinueOnError)
	fs.SetOutput(w)
	target := fs.String("vm", "", "target VM name")
	fs.Usage = func() { printCompactUsage(w) }
	return fs, target
}

func compactVM(vmDirectory string) (*compactResult, error) {
	client := NewControlClient(GetControlSocketPathForVM(vmDirectory))
	client.SetTimeout(5 * time.Second)
	return compactVMWithClient(vmDirectory, client)
}

func compactVMWithClient(vmDirectory string, client compactClient) (*compactResult, error) {
	if _, err := client.AgentPingTyped(); err != nil {
		return nil, fmt.Errorf("guest agent unavailable: %w", err)
	}

	platform := agentstate.Platform(vmDirectory)
	args, err := compactCommand(platform)
	if err != nil {
		return nil, err
	}

	res, err := client.AgentExecTypedTimeout(args, nil, "", compactTimeout)
	if err != nil {
		return nil, fmt.Errorf("compact guest: %w", err)
	}
	if res == nil {
		return nil, fmt.Errorf("compact guest: missing response")
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(res.Stderr)
		if msg == "" {
			msg = strings.TrimSpace(res.Stdout)
		}
		if msg == "" {
			msg = "unknown error"
		}
		return nil, fmt.Errorf("compact guest: exit %d: %s", res.ExitCode, msg)
	}

	return &compactResult{
		Platform: platform,
		Stdout:   strings.TrimSpace(res.Stdout),
		Stderr:   strings.TrimSpace(res.Stderr),
	}, nil
}

func compactCommand(platform string) ([]string, error) {
	switch platform {
	case agentstate.PlatformLinux:
		return []string{"fstrim", "-v", "/"}, nil
	case agentstate.PlatformMacOS:
		return []string{"diskutil", "secureErase", "freespace", "0", "/"}, nil
	default:
		return nil, fmt.Errorf("unsupported guest platform %q", platform)
	}
}

func printCompactResult(w io.Writer, result *compactResult) {
	fmt.Fprintf(w, "Compacted %s guest free space\n", result.Platform)
	if result.Stdout != "" {
		fmt.Fprintln(w, result.Stdout)
	}
	if result.Stderr != "" {
		fmt.Fprintln(w, result.Stderr)
	}
}
