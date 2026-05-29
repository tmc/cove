package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/tmc/cove/internal/agentsandbox"
	"github.com/tmc/cove/internal/controlclient"
	"github.com/tmc/cove/internal/guibench"
)

// vzForkBackend is the reference guibench.Backend: it scores a corpus by
// forking a fresh ephemeral RAM-overlay cove VM per task (design 047 §6, §9
// slice 2). Acquire execs `cove run -fork-from <image> -ephemeral`, exactly the
// agent-sandbox fork path (agent_sandbox.go), waits for the guest agent, and
// hands the engine a Probe over the fork's control socket; Close discards the
// fork. The RAM-overlay's throw-everything-away property is the per-task reset.
type vzForkBackend struct {
	coveBin  string
	provider string
	maxTier  guibench.Tier
	stdout   io.Writer
	stderr   io.Writer
	seq      int
}

// newVZForkBackend resolves the running cove binary and returns a backend that
// forks from it. maxTier is the privilege level the operator certifies the base
// image carries (verified by `cove doctor` before save, design 047 §5); the
// engine refuses a corpus that needs more.
func newVZForkBackend(provider string, maxTier guibench.Tier, stdout, stderr io.Writer) (*vzForkBackend, error) {
	bin, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve cove binary: %w", err)
	}
	return &vzForkBackend{
		coveBin:  bin,
		provider: provider,
		maxTier:  maxTier,
		stdout:   stdout,
		stderr:   stderr,
	}, nil
}

func (b *vzForkBackend) MaxTier() guibench.Tier { return b.maxTier }

// Acquire forks a fresh ephemeral VM from image and returns a session bound to
// it. The fork name is unique per acquisition so concurrent or sequential forks
// never collide; the VM is never reused across tasks (design 047 §6, §12).
func (b *vzForkBackend) Acquire(ctx context.Context, image string) (guibench.Session, error) {
	if strings.TrimSpace(image) == "" {
		return nil, errors.New("vz backend: task image is empty")
	}
	suffix, err := generateRunID()
	if err != nil {
		return nil, err
	}
	b.seq++
	vm := fmt.Sprintf("guibench-%s-%d", suffix, b.seq)

	runCmd := exec.CommandContext(ctx, b.coveBin, "run",
		"-fork-from", image, "-fork-name", vm, "-ephemeral", "-auto-upgrade-agent")
	runCmd.Stdout = b.stderr // fork chatter goes to stderr; stdout stays clean for score.json
	runCmd.Stderr = b.stderr
	if err := runCmd.Start(); err != nil {
		return nil, fmt.Errorf("vz backend: start fork %s: %w", vm, err)
	}

	if err := b.waitReady(ctx, vm); err != nil {
		b.stopVM(context.Background(), vm)
		reapForkRun(runCmd)
		return nil, fmt.Errorf("vz backend: %w", err)
	}

	dir, err := requireExistingVMForControl(vm)
	if err != nil {
		b.stopVM(context.Background(), vm)
		reapForkRun(runCmd)
		return nil, fmt.Errorf("vz backend: resolve fork %s: %w", vm, err)
	}
	client := controlclient.New(GetControlSocketPathForVM(dir))
	client.SetTimeout(60 * time.Second)

	return &vzForkSession{
		backend: b,
		vm:      vm,
		runCmd:  runCmd,
		client:  client,
		probe:   guibench.ClientProbe{Client: client},
	}, nil
}

// waitReady blocks until the fork's guest agent answers a ping, mirroring
// waitAgentSandboxReady (agent_sandbox.go).
func (b *vzForkBackend) waitReady(ctx context.Context, vm string) error {
	cmd := exec.CommandContext(ctx, b.coveBin, "ctl", "-vm", vm, "-wait", "120s", "agent-ping")
	cmd.Stdout = b.stderr
	cmd.Stderr = b.stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("wait for guest agent on %s: %w", vm, err)
	}
	return nil
}

// stopVM stops the fork, discarding the RAM overlay (the per-task reset).
func (b *vzForkBackend) stopVM(ctx context.Context, vm string) {
	cmd := exec.CommandContext(ctx, b.coveBin, "ctl", "-vm", vm, "stop")
	cmd.Stdout = b.stderr
	cmd.Stderr = b.stderr
	_ = cmd.Run()
}

// vzForkSession is one task's hold on a forked VM.
type vzForkSession struct {
	backend *vzForkBackend
	vm      string
	runCmd  *exec.Cmd
	client  *controlclient.Client
	probe   guibench.Probe
}

func (s *vzForkSession) Probe() guibench.Probe { return s.probe }

// RunAgent runs the provider's computer-use loop against the fork via
// agentsandbox.Run (the same loop agent-sandbox uses, agent_sandbox.go), and
// returns the agent's final answer for the verifier to score.
func (s *vzForkSession) RunAgent(ctx context.Context, instruction string, budget int) (string, error) {
	res, err := agentsandbox.Run(ctx, agentsandbox.Options{
		Provider: s.backend.provider,
		VMName:   s.vm,
		Task:     instruction,
		MaxSteps: budget,
		Stdout:   s.backend.stderr,
		Stderr:   s.backend.stderr,
	})
	if err != nil {
		return "", fmt.Errorf("provider %s: %w", s.backend.provider, err)
	}
	return res.FinalAnswer, nil
}

// Close stops the fork (discarding its RAM overlay) and reaps the run process.
func (s *vzForkSession) Close() error {
	s.backend.stopVM(context.Background(), s.vm)
	reapForkRun(s.runCmd)
	return nil
}

// reapForkRun waits for a forked `cove run` process to exit, killing it if it
// does not stop promptly, so a discarded fork never leaks a process.
func reapForkRun(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		_ = cmd.Process.Kill()
		<-done
	}
}
