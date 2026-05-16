package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/tmc/vz-macos/internal/metrics"
)

type execOptions struct {
	interactive bool
	tty         bool
	env         secretEnvFlag
	secretEnv   secretEnvFlag
	workDir     string
	user        string
}

func execCommand(args []string) error {
	opts, vm, argv, err := parseExecArgs(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	sock, err := resolveShellSocket(vm)
	if err != nil {
		return err
	}
	token := resolveControlTokenForSocket(sock)

	masker := metrics.NewMasker()
	env, err := resolveShellEnv(opts.env, opts.secretEnv, masker, os.Stderr)
	if err != nil {
		return err
	}

	stdin := os.Stdin
	var devnull *os.File
	if !opts.interactive {
		devnull, err = os.Open(os.DevNull)
		if err != nil {
			return fmt.Errorf("open stdin: %w", err)
		}
		defer devnull.Close()
		stdin = devnull
	}

	exitCode, err := runShellSession(context.Background(), sock, token, vm, argv, env, masker, shellSessionOptions{
		TTY:         opts.tty,
		Interactive: opts.interactive,
		User:        opts.user,
		WorkingDir:  opts.workDir,
	}, stdin, os.Stdout, os.Stderr)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		os.Exit(int(exitCode))
	}
	return nil
}

func parseExecArgs(args []string) (execOptions, string, []string, error) {
	args = normalizeExecShortFlags(args)

	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { printExecUsage(os.Stderr) }

	var opts execOptions
	fs.BoolVar(&opts.interactive, "i", false, "keep stdin open")
	fs.BoolVar(&opts.interactive, "interactive", false, "keep stdin open")
	fs.BoolVar(&opts.tty, "t", false, "allocate a guest TTY")
	fs.BoolVar(&opts.tty, "tty", false, "allocate a guest TTY")
	fs.Var(&opts.env, "e", "guest env NAME=value (repeatable)")
	fs.Var(&opts.env, "env", "guest env NAME=value (repeatable)")
	fs.Var(&opts.secretEnv, "secret-env", "guest env NAME=value|env://VAR|file:///path (repeatable; redacted in run logs)")
	fs.StringVar(&opts.workDir, "w", "", "working directory in the guest")
	fs.StringVar(&opts.workDir, "workdir", "", "working directory in the guest")
	fs.StringVar(&opts.user, "u", "", "user to run as in the guest")
	fs.StringVar(&opts.user, "user", "", "user to run as in the guest")

	if err := fs.Parse(args); err != nil {
		return execOptions{}, "", nil, err
	}
	tail := fs.Args()
	if len(tail) == 0 {
		fs.Usage()
		return execOptions{}, "", nil, fmt.Errorf("usage: cove exec [options] <vm> <cmd> [args...]")
	}
	vm := tail[0]
	argv := append([]string{}, tail[1:]...)
	if len(argv) > 0 && argv[0] == "--" {
		argv = argv[1:]
	}
	if len(argv) == 0 {
		fs.Usage()
		return execOptions{}, "", nil, fmt.Errorf("exec requires a command")
	}
	return opts, vm, argv, nil
}

func normalizeExecShortFlags(args []string) []string {
	out := make([]string, 0, len(args)+2)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			out = append(out, args[i:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			out = append(out, args[i:]...)
			break
		}
		switch arg {
		case "-it", "-ti":
			out = append(out, "-i", "-t")
		case "-e", "--env", "-secret-env", "--secret-env", "-w", "--workdir", "-u", "--user":
			out = append(out, arg)
			if i+1 < len(args) {
				i++
				out = append(out, args[i])
			}
		default:
			out = append(out, arg)
		}
	}
	return out
}

func printExecUsage(w io.Writer) {
	fmt.Fprint(w, `Usage: cove exec [options] <vm> <cmd> [args...]

Run a command in a running VM through the guest agent.

Options:
`)
	fmt.Fprint(w, `  -i, --interactive        keep stdin open
  -t, --tty                allocate a guest TTY
  -e, --env NAME=value     set guest environment variable (repeatable)
      --secret-env SPEC    set redacted guest environment variable
  -w, --workdir DIR        run in guest working directory
  -u, --user USER          run as guest user

Examples:
  cove exec ubuntu uname -a
  cove exec -it ubuntu bash
  cove exec -e CI=1 -w /work ubuntu go test ./...
  cove exec ubuntu -- sh -lc 'echo "$0"'
`)
}

func runExecCommand(env commandEnv, _ string, args []string) int {
	err := execCommand(args)
	if err != nil && (strings.HasPrefix(err.Error(), "usage: cove exec ") || strings.Contains(err.Error(), "requires a command")) {
		return commandUsageError(env, err)
	}
	return commandError(env, err)
}
