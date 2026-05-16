package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	fleetpkg "github.com/tmc/vz-macos/internal/fleet"
)

type fleetRunner interface {
	Run(ctx context.Context, remote fleetpkg.Remote, args []string, stdout, stderr io.Writer) error
}

type fleetCommandRunner interface {
	Run(ctx context.Context, remote fleetpkg.Remote, args []string, stdin io.Reader, stdout, stderr io.Writer) error
}

type sshFleetRunner struct{}

var (
	fleetDialControlSocket = fleetpkg.DialControlSocket
	fleetReadControlToken  = fleetpkg.ReadControlToken
	fleetCtlCommand        = ctlCommand
)

func handleFleetCommand(args []string) error {
	return runFleetCommand(args, fleetpkg.DefaultPath(), os.Stdout)
}

func runFleetCommand(args []string, path string, out io.Writer) error {
	return runFleetCommandWithRunner(context.Background(), args, path, sshFleetRunner{}, out, os.Stderr)
}

func runFleetCommandWithRunner(ctx context.Context, args []string, path string, runner fleetRunner, out, errOut io.Writer) error {
	if len(args) == 0 {
		printFleetUsage(errOut)
		return errors.New("fleet: command required")
	}
	if isHelpArg(args[0]) {
		printFleetUsage(out)
		return nil
	}
	switch args[0] {
	case "add":
		return fleetAdd(args[1:], path)
	case "ls", "list":
		if len(args) > 1 && isHelpArg(args[1]) {
			fmt.Fprintln(out, "Usage: cove fleet ls")
			return nil
		}
		return fleetList(path, out)
	case "rm", "remove":
		if len(args) > 1 && isHelpArg(args[1]) {
			fmt.Fprintln(out, "Usage: cove fleet rm <name>")
			return nil
		}
		return fleetRemove(args[1:], path)
	case "vm", "image":
		if len(args) > 1 && isHelpArg(args[1]) {
			fmt.Fprintf(out, "Usage: cove fleet %s ls [--json]\n", args[0])
			return nil
		}
		return runFleetAggregateCommand(ctx, args, path, runner, out, errOut)
	case "ps":
		return runFleetPSCommand(ctx, args[1:], path, runner, out, errOut)
	case "run":
		if len(args) > 1 && isHelpArg(args[1]) {
			fmt.Fprintln(out, "Usage: cove fleet run --policy=least-loaded [run flags]")
			return nil
		}
		return runFleetRunCommand(ctx, args[1:], path, runner, out, errOut)
	case "metrics":
		return runFleetMetricsCommand(ctx, args[1:], path, runner, out, errOut)
	default:
		if ok, err := fleetHasRemote(args[0], path); err != nil {
			return err
		} else if ok && len(args) >= 2 {
			return runFleetRoute(ctx, args[0], args[1], args[2:], path, runner, out, errOut)
		}
		return fmt.Errorf("fleet: unknown command %q", args[0])
	}
}

func fleetHasRemote(name, path string) (bool, error) {
	cfg, err := fleetpkg.LoadPath(path)
	if err != nil {
		return false, err
	}
	_, ok := cfg.Get(name)
	return ok, nil
}

func printFleetUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove fleet <command> [args]

Commands:
  add <name> <user@host> [-vm <default>]
  ls
  rm <name>
  vm ls [--json]
  image ls [--json]
  ps
  run --policy=least-loaded [run flags]
  metrics
  <remote> <command> [args...]`)
}

func fleetAdd(args []string, path string) error {
	fs := flag.NewFlagSet("fleet add", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	defaultVM := fs.String("vm", "", "default VM for this remote")
	if done, err := parseFlagsOrHelpExit(fs, moveFleetAddFlagsFirst(args)); done || err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("usage: cove fleet add <name> <user@host> [-vm <default>]")
	}
	remote, err := fleetpkg.ParseTarget(fs.Arg(1))
	if err != nil {
		return err
	}
	remote.DefaultVM = *defaultVM
	cfg, err := fleetpkg.LoadPath(path)
	if err != nil {
		return err
	}
	if err := cfg.Add(fs.Arg(0), remote); err != nil {
		return err
	}
	return fleetpkg.SavePath(path, cfg)
}

func moveFleetAddFlagsFirst(args []string) []string {
	var flags, rest []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-vm", "--vm":
			flags = append(flags, args[i])
			if i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
		default:
			rest = append(rest, args[i])
		}
	}
	return append(flags, rest...)
}

func fleetList(path string, out io.Writer) error {
	cfg, err := fleetpkg.LoadPath(path)
	if err != nil {
		return err
	}
	entries := cfg.List()
	if len(entries) == 0 {
		fmt.Fprintln(out, "no fleet remotes")
		return nil
	}
	for _, e := range entries {
		target := e.Host
		if e.User != "" {
			target = e.User + "@" + e.Host
		}
		if e.DefaultVM != "" {
			fmt.Fprintf(out, "%s\t%s\tdefault_vm=%s\n", e.Name, target, e.DefaultVM)
		} else {
			fmt.Fprintf(out, "%s\t%s\n", e.Name, target)
		}
	}
	return nil
}

func fleetRemove(args []string, path string) error {
	if len(args) != 1 {
		return errors.New("usage: cove fleet rm <name>")
	}
	cfg, err := fleetpkg.LoadPath(path)
	if err != nil {
		return err
	}
	if err := cfg.Remove(args[0]); err != nil {
		return err
	}
	return fleetpkg.SavePath(path, cfg)
}

func handleFleetRoute(ctx context.Context, name, cmd string, args []string) error {
	return runFleetRoute(ctx, name, cmd, args, fleetpkg.DefaultPath(), sshFleetRunner{}, os.Stdout, os.Stderr)
}

func runFleetRoute(ctx context.Context, name, cmd string, args []string, path string, runner fleetRunner, stdout, stderr io.Writer) error {
	cfg, err := fleetpkg.LoadPath(path)
	if err != nil {
		return err
	}
	remote, ok := cfg.Get(name)
	if !ok {
		return fmt.Errorf("fleet: remote %q not found", name)
	}
	if cmd == "ctl" {
		return runFleetControlRoute(ctx, remote, args)
	}
	argv, err := fleetRemoteArgs(cmd, args, remote)
	if err != nil {
		return err
	}
	return runner.Run(ctx, remote, argv, stdout, stderr)
}

func runFleetControlRoute(ctx context.Context, remote fleetpkg.Remote, args []string) error {
	vm := fleetControlVM(args, remote)
	tunnel, err := fleetDialControlSocket(ctx, remote, vm)
	if err != nil {
		return err
	}
	defer tunnel.Close()
	token, err := fleetReadControlToken(ctx, remote, vm)
	if err != nil {
		return err
	}
	return fleetCtlCommand(append([]string{"-socket", tunnel.LocalSocket(), "-token", token}, args...))
}

func fleetControlVM(args []string, remote fleetpkg.Remote) string {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-vm" || arg == "--vm":
			if i+1 < len(args) {
				return args[i+1]
			}
		case strings.HasPrefix(arg, "-vm="):
			return strings.TrimPrefix(arg, "-vm=")
		case strings.HasPrefix(arg, "--vm="):
			return strings.TrimPrefix(arg, "--vm=")
		}
	}
	return remote.DefaultVM
}

func fleetRemoteArgs(cmd string, args []string, remote fleetpkg.Remote) ([]string, error) {
	switch cmd {
	case "shell":
		return append([]string{"shell"}, ensureFleetPositionalVM(args, remote)...), nil
	case "cp":
		return append([]string{"cp"}, args...), nil
	case "logs":
		return append([]string{"logs"}, ensureFleetPositionalVM(args, remote)...), nil
	case "list", "ls":
		return []string{"list"}, nil
	case "vm":
		if len(args) == 0 || args[0] == "list" || args[0] == "ls" {
			return append([]string{"vm"}, args...), nil
		}
	case "image":
		if len(args) > 0 && (args[0] == "list" || args[0] == "ls") {
			return append([]string{"image"}, args...), nil
		}
	case "run":
		return append([]string{"run"}, args...), nil
	}
	return nil, fmt.Errorf("fleet: command %q is not supported in fleet routing", cmd)
}

func ensureFleetPositionalVM(args []string, remote fleetpkg.Remote) []string {
	if len(args) > 0 || remote.DefaultVM == "" {
		return args
	}
	return []string{remote.DefaultVM}
}

func (sshFleetRunner) Run(ctx context.Context, remote fleetpkg.Remote, args []string, stdout, stderr io.Writer) error {
	return runSSHFleetCommand(ctx, remote, args, nil, stdout, stderr)
}

func (sshFleetRunner) RunCommand(ctx context.Context, remote fleetpkg.Remote, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return runSSHFleetCommand(ctx, remote, args, stdin, stdout, stderr)
}

func runSSHFleetCommand(ctx context.Context, remote fleetpkg.Remote, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	sshArgs := append([]string{}, remote.SSHArgs...)
	sshArgs = append(sshArgs, fleetRemoteTarget(remote), "cove")
	sshArgs = append(sshArgs, shellJoinArgs(args))
	cmd := exec.CommandContext(ctx, fleetSSHBinary(), sshArgs...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

func fleetSSHBinary() string {
	if path := os.Getenv("COVE_FLEET_SSH"); path != "" {
		return path
	}
	return "ssh"
}

func fleetRemoteTarget(remote fleetpkg.Remote) string {
	if remote.User != "" {
		return remote.User + "@" + remote.Host
	}
	return remote.Host
}

func shellJoinArgs(args []string) string {
	parts := append([]string(nil), args...)
	for i, arg := range parts {
		parts[i] = shellQuote(arg)
	}
	return strings.Join(parts, " ")
}
