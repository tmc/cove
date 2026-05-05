package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var (
	execCommandContext = exec.CommandContext
	cleanupWait        = 5 * time.Second
)

type config struct {
	CoveBin    string
	Image      string
	Command    string
	Script     string
	VMName     string
	Timeout    time.Duration
	ReadyEvery time.Duration
	Keep       bool
	Env        []string
	Stdout     io.Writer
	Stderr     io.Writer
	Environ    []string
}

func main() {
	os.Exit(run(os.Args[1:], os.Environ(), os.Stdout, os.Stderr))
}

func run(args, environ []string, stdout, stderr io.Writer) int {
	cfg, err := parseConfig(args, environ, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "cove-action: %v\n", err)
		return 2
	}
	code, err := runJob(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "cove-action: %v\n", err)
		if code != 0 {
			_ = writeOutputs(cfg, code, defaultLogPath(cfg.Environ))
			return code
		}
		return 1
	}
	if err := writeOutputs(cfg, code, defaultLogPath(cfg.Environ)); err != nil {
		fmt.Fprintf(stderr, "cove-action: write outputs: %v\n", err)
		return 1
	}
	return code
}

func parseConfig(args, environ []string, stdout, stderr io.Writer) (config, error) {
	cfg := config{
		CoveBin:    envValue(environ, "COVE_ACTION_COVE_BIN", envValue(environ, "COVE_BIN", "cove")),
		Image:      envValue(environ, "COVE_ACTION_IMAGE", ""),
		Command:    envValue(environ, "COVE_ACTION_COMMAND", envValue(environ, "COVE_ARGS", "")),
		Script:     envValue(environ, "COVE_ACTION_SCRIPT", envValue(environ, "COVE_SCRIPT", "")),
		VMName:     envValue(environ, "COVE_ACTION_VM_NAME", ""),
		ReadyEvery: time.Second,
		Stdout:     stdout,
		Stderr:     stderr,
		Environ:    environ,
	}
	timeoutText := envValue(environ, "COVE_ACTION_TIMEOUT", envValue(environ, "COVE_TIMEOUT", "30m"))
	keepText := envValue(environ, "COVE_ACTION_KEEP", "false")
	envText := envValue(environ, "COVE_ACTION_ENV", "")

	fs := flag.NewFlagSet("cove-action", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.CoveBin, "cove-bin", cfg.CoveBin, "cove binary path")
	fs.StringVar(&cfg.Image, "image", cfg.Image, "local cove image to fork from")
	fs.StringVar(&cfg.Command, "command", cfg.Command, "guest shell command")
	fs.StringVar(&cfg.Command, "args", cfg.Command, "guest shell command")
	fs.StringVar(&cfg.Script, "script", cfg.Script, "guest shell script")
	fs.StringVar(&cfg.VMName, "vm-name", cfg.VMName, "ephemeral VM name")
	fs.StringVar(&timeoutText, "timeout", timeoutText, "overall timeout")
	fs.BoolVar(&cfg.Keep, "keep", parseBool(keepText), "keep ephemeral fork")
	fs.StringVar(&envText, "env", envText, "newline-separated KEY=VALUE guest environment")
	if err := fs.Parse(args); err != nil {
		return cfg, err
	}
	if fs.NArg() != 0 {
		return cfg, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	timeout, err := time.ParseDuration(timeoutText)
	if err != nil || timeout <= 0 {
		return cfg, fmt.Errorf("invalid timeout %q", timeoutText)
	}
	cfg.Timeout = timeout
	cfg.Env, err = parseEnvBlock(envText)
	if err != nil {
		return cfg, err
	}
	if strings.TrimSpace(cfg.Image) == "" {
		return cfg, errors.New("image is required")
	}
	if strings.TrimSpace(cfg.Command) == "" && strings.TrimSpace(cfg.Script) == "" {
		return cfg, errors.New("command or script is required")
	}
	if strings.TrimSpace(cfg.VMName) == "" {
		cfg.VMName = defaultVMName(environ)
	}
	return cfg, nil
}

func runJob(cfg config) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	runArgs := []string{"run", "-fork-from", cfg.Image, "-fork-name", cfg.VMName, "-ephemeral", "-headless"}
	if cfg.Keep {
		runArgs = append(runArgs, "-keep")
	}
	runCmd := execCommandContext(ctx, cfg.CoveBin, runArgs...)
	runCmd.Stdout = cfg.Stdout
	runCmd.Stderr = cfg.Stderr
	runCmd.Env = cfg.Environ
	if err := runCmd.Start(); err != nil {
		return 0, fmt.Errorf("start cove run: %w", err)
	}

	runDone := make(chan error, 1)
	go func() {
		runDone <- runCmd.Wait()
	}()
	defer cleanup(cfg, runCmd, runDone)

	if err := waitReady(ctx, cfg); err != nil {
		return 1, err
	}
	code, err := execGuestCommand(ctx, cfg)
	if err != nil {
		return code, err
	}
	return code, nil
}

func waitReady(ctx context.Context, cfg config) error {
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for guest readiness: %w", ctx.Err())
		default:
		}

		probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := runShell(probeCtx, cfg, "true")
		cancel()
		if err == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for guest readiness: %w", ctx.Err())
		case <-time.After(cfg.ReadyEvery):
		}
	}
}

func execGuestCommand(ctx context.Context, cfg config) (int, error) {
	command := cfg.Command
	if strings.TrimSpace(cfg.Script) != "" {
		command = cfg.Script
	}
	if len(cfg.Env) > 0 {
		var b strings.Builder
		b.WriteString("env")
		for _, kv := range cfg.Env {
			b.WriteByte(' ')
			b.WriteString(shellQuote(kv))
		}
		b.WriteString(" /bin/sh -lc ")
		b.WriteString(shellQuote(command))
		command = b.String()
	}
	err := runShell(ctx, cfg, command)
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return 124, fmt.Errorf("guest command timed out: %w", ctx.Err())
	}
	return 1, fmt.Errorf("guest command: %w", err)
}

func runShell(ctx context.Context, cfg config, command string) error {
	cmd := execCommandContext(ctx, cfg.CoveBin, "shell", cfg.VMName, "--", "/bin/sh", "-lc", command)
	cmd.Stdout = cfg.Stdout
	cmd.Stderr = cfg.Stderr
	cmd.Env = cfg.Environ
	return cmd.Run()
}

func cleanup(cfg config, runCmd *exec.Cmd, runDone <-chan error) {
	stopCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stop := execCommandContext(stopCtx, cfg.CoveBin, "ctl", "-vm", cfg.VMName, "stop")
	stop.Stdout = cfg.Stdout
	stop.Stderr = cfg.Stderr
	stop.Env = cfg.Environ
	_ = stop.Run()

	select {
	case <-runDone:
		return
	case <-time.After(cleanupWait):
	}
	if runCmd.Process != nil {
		_ = runCmd.Process.Signal(syscall.SIGTERM)
	}
	select {
	case <-runDone:
	case <-time.After(cleanupWait):
		if runCmd.Process != nil {
			_ = runCmd.Process.Kill()
		}
		<-runDone
	}
}

func writeOutputs(cfg config, exitCode int, logPath string) error {
	path := envValue(cfg.Environ, "GITHUB_OUTPUT", "")
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o666)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "vm-name=%s\nexit-code=%d\nlog-path=%s\n", cfg.VMName, exitCode, logPath)
	return err
}

func parseEnvBlock(s string) ([]string, error) {
	var env []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.Contains(line, "=") {
			return nil, fmt.Errorf("invalid env entry %q", line)
		}
		env = append(env, line)
	}
	return env, nil
}

func envValue(environ []string, key, fallback string) string {
	prefix := key + "="
	for _, kv := range environ {
		if strings.HasPrefix(kv, prefix) {
			return strings.TrimPrefix(kv, prefix)
		}
	}
	return fallback
}

func parseBool(s string) bool {
	ok, err := strconv.ParseBool(strings.TrimSpace(s))
	return err == nil && ok
}

func defaultVMName(environ []string) string {
	runID := envValue(environ, "GITHUB_RUN_ID", "local")
	attempt := envValue(environ, "GITHUB_RUN_ATTEMPT", "1")
	name := "cove-action-" + runID + "-" + attempt
	name = regexp.MustCompile(`[^A-Za-z0-9_.-]+`).ReplaceAllString(name, "-")
	if len(name) > 63 {
		name = name[:63]
	}
	return strings.Trim(name, "-.")
}

func defaultLogPath(environ []string) string {
	home := envValue(environ, "HOME", "")
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = h
		}
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".vz", "runs")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
