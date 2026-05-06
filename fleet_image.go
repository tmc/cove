package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	fleetpkg "github.com/tmc/vz-macos/internal/fleet"
)

type localFleetRemoteRunner struct{}

func (localFleetRemoteRunner) Run(ctx context.Context, remote fleetpkg.Remote, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if isLocalFleetRemote(remote) {
		return runLocalCoveCommand(args, stdin, stdout, stderr)
	}
	return sshFleetRunner{}.RunCommand(ctx, remote, args, stdin, stdout, stderr)
}

func runFleetImageTransferCommand(ctx context.Context, args []string, path string, runner fleetRunner, out, errOut io.Writer) error {
	commandRunner := fleetCommandRunner(localFleetRemoteRunner{})
	if r, ok := runner.(interface {
		RunCommand(context.Context, fleetpkg.Remote, []string, io.Reader, io.Writer, io.Writer) error
	}); ok {
		commandRunner = commandRunnerFunc(r.RunCommand)
	}
	if len(args) == 0 {
		return fmt.Errorf("usage: cove fleet image push <ref> <dst-host> | pull <ref> <src-host> | sync <ref> <src-host> <dst-host>")
	}
	cfg, err := fleetpkg.LoadPath(path)
	if err != nil {
		return err
	}
	switch args[0] {
	case "push":
		fs := flag.NewFlagSet("fleet image push", flag.ContinueOnError)
		fs.SetOutput(errOut)
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 2 {
			return fmt.Errorf("usage: cove fleet image push <ref> <dst-host>")
		}
		dst, err := fleetRemoteByName(cfg, fs.Arg(1))
		if err != nil {
			return err
		}
		if err := fleetpkg.PushImage(ctx, fs.Arg(0), fleetpkg.Remote{}, dst, commandRunner); err != nil {
			return err
		}
		fmt.Fprintf(out, "pushed image %s to %s\n", fs.Arg(0), fs.Arg(1))
		return nil
	case "pull":
		fs := flag.NewFlagSet("fleet image pull", flag.ContinueOnError)
		fs.SetOutput(errOut)
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 2 {
			return fmt.Errorf("usage: cove fleet image pull <ref> <src-host>")
		}
		src, err := fleetRemoteByName(cfg, fs.Arg(1))
		if err != nil {
			return err
		}
		if err := fleetpkg.PullImage(ctx, fs.Arg(0), src, fleetpkg.Remote{}, commandRunner); err != nil {
			return err
		}
		fmt.Fprintf(out, "pulled image %s from %s\n", fs.Arg(0), fs.Arg(1))
		return nil
	case "sync":
		fs := flag.NewFlagSet("fleet image sync", flag.ContinueOnError)
		fs.SetOutput(errOut)
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 3 {
			return fmt.Errorf("usage: cove fleet image sync <ref> <src-host> <dst-host>")
		}
		src, err := fleetRemoteByName(cfg, fs.Arg(1))
		if err != nil {
			return err
		}
		dst, err := fleetRemoteByName(cfg, fs.Arg(2))
		if err != nil {
			return err
		}
		if err := fleetpkg.TransferImage(ctx, fs.Arg(0), src, dst, commandRunner); err != nil {
			return err
		}
		fmt.Fprintf(out, "synced image %s from %s to %s\n", fs.Arg(0), fs.Arg(1), fs.Arg(2))
		return nil
	default:
		return fmt.Errorf("fleet: unknown image command %q", args[0])
	}
}

func fleetRemoteByName(cfg *fleetpkg.Config, name string) (fleetpkg.Remote, error) {
	remote, ok := cfg.Get(name)
	if !ok {
		return fleetpkg.Remote{}, fmt.Errorf("fleet: remote %q not found", name)
	}
	return remote, nil
}

type commandRunnerFunc func(context.Context, fleetpkg.Remote, []string, io.Reader, io.Writer, io.Writer) error

func (f commandRunnerFunc) Run(ctx context.Context, remote fleetpkg.Remote, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return f(ctx, remote, args, stdin, stdout, stderr)
}
