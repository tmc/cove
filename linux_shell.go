// linux_shell.go - Host-side wrapper that exercises the unary exec control
// RPCs (ExecStream with tty=true, ResizeExecTTY, SignalExec) by piping a
// guest shell through to the host terminal during `cove run -linux -shell`.
//
// Limitation: the agent's ExecStream RPC is server-streaming only, so stdin
// from the host TTY cannot be sent to the guest after the request is opened.
// This wrapper is therefore output-only — useful for tail/log following and
// for validating the SIGWINCH/SIGINT control plane end-to-end. Bidi stdin is
// a v0.3 concern (see design 023).
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/term"

	agentstate "github.com/tmc/vz-macos/internal/agent"
)

// linuxShellCommand is the guest-side command launched when `-shell` is set.
// Kept as a var so a future flag can override it.
var linuxShellCommand = []string{"/bin/bash", "-l"}

// runLinuxShellSession opens an Exec stream against the guest agent with
// tty=true, forwards its output to the host terminal, and installs SIGWINCH /
// SIGINT handlers that translate to ResizeExecTTY / SignalExec on the agent.
//
// It blocks until the stream closes (guest shell exits, agent disconnects,
// or ctx is cancelled). The returned error is the first non-EOF stream error
// or context error; nil on a clean guest-side exit.
//
// The control server must already be wired to the running VM (vm + queue +
// vmDir). The shell connects on demand via the existing connectAgent path,
// so the agent need not have answered Ping yet at call time.
func runLinuxShellSession(ctx context.Context, cs *ControlServer) error {
	if cs == nil {
		return errors.New("nil control server")
	}

	hostFD := int(os.Stdin.Fd())
	if !term.IsTerminal(hostFD) {
		return errors.New("-shell requires stdin to be a terminal")
	}

	// Wait for the agent to come up; the daemon listens on vsock 1024 once
	// the guest has finished early boot. checkAgentAvailability already
	// runs concurrently from runVMHeadless, but we re-poll here so we can
	// surface a clear error if it never arrives.
	agent, err := waitForAgentReady(ctx, cs, 90*time.Second)
	if err != nil {
		return fmt.Errorf("agent not ready: %w", err)
	}

	execID := fmt.Sprintf("cove-shell-%d", time.Now().UnixNano())

	stream, err := agent.ExecStreamControl(ctx, execID, true, "", linuxShellCommand, nil, "")
	if err != nil {
		return fmt.Errorf("open exec stream: %w", err)
	}

	// Switch host TTY to raw mode so terminal control sequences from the
	// guest (cursor moves, colors) are not mangled by line discipline.
	prev, err := term.MakeRaw(hostFD)
	if err != nil {
		return fmt.Errorf("raw mode: %w", err)
	}
	defer func() {
		_ = term.Restore(hostFD, prev)
	}()

	// Send the initial winsize so the guest pty matches the host terminal
	// before any bytes are written. Best-effort: log on failure but keep going.
	if cols, rows, sizeErr := term.GetSize(hostFD); sizeErr == nil {
		if rsErr := agent.ResizeExec(ctx, execID, uint32(rows), uint32(cols)); rsErr != nil {
			fmt.Fprintf(os.Stderr, "cove shell: initial resize: %v\r\n", rsErr)
		}
	}

	// SIGWINCH and SIGINT only land on the host; install handlers and
	// translate to the corresponding unary RPC. We use buffered channels of
	// size 1 because bursts collapse into the most recent event for both
	// signals (SIGWINCH coalesces; SIGINT is idempotent on the guest side).
	winchCh := make(chan os.Signal, 1)
	intCh := make(chan os.Signal, 1)
	signal.Notify(winchCh, syscall.SIGWINCH)
	signal.Notify(intCh, syscall.SIGINT)
	defer signal.Stop(winchCh)
	defer signal.Stop(intCh)

	signalCtx, cancelSignals := context.WithCancel(ctx)
	defer cancelSignals()
	go forwardWinsize(signalCtx, agent, execID, hostFD, winchCh)
	go forwardInterrupt(signalCtx, agent, execID, intCh)

	// Pump the agent stream into stdout. The kernel folded child stderr into
	// the same pty (see 9c5f58d), so STDOUT is the only stream we receive.
	for {
		out, recvErr := stream.Recv()
		if recvErr != nil {
			if errors.Is(recvErr, io.EOF) {
				return nil
			}
			return fmt.Errorf("recv: %w", recvErr)
		}
		if len(out.Data) > 0 {
			if _, wErr := os.Stdout.Write(out.Data); wErr != nil {
				return fmt.Errorf("write stdout: %w", wErr)
			}
		}
		if out.ExitCode != nil {
			return nil
		}
	}
}

// shellResizer is the subset of the agent client used by forwardWinsize.
// It exists so tests can substitute a recording fake without spinning up
// a real connect-go server.
type shellResizer interface {
	ResizeExec(ctx context.Context, execID string, rows, cols uint32) error
}

// shellSignaler is the subset of the agent client used by forwardInterrupt.
type shellSignaler interface {
	SignalExec(ctx context.Context, execID string, signal int32) error
}

// forwardWinsize reads SIGWINCH events and issues ResizeExecTTY on each one.
// Returns when ctx is cancelled or the channel is closed.
func forwardWinsize(ctx context.Context, agent shellResizer, execID string, hostFD int, ch <-chan os.Signal) {
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-ch:
			if !ok {
				return
			}
		}
		cols, rows, err := term.GetSize(hostFD)
		if err != nil {
			continue
		}
		if err := agent.ResizeExec(ctx, execID, uint32(rows), uint32(cols)); err != nil {
			// Resize is best-effort; surface the error but stay running.
			fmt.Fprintf(os.Stderr, "cove shell: resize: %v\r\n", err)
		}
	}
}

// hostSignalToExecSignal maps a host os.Signal we listen for into the
// integer signal value sent to the guest via SignalExec. The agent only
// accepts SIGINT, SIGTERM, and SIGKILL; any other host signal is reported
// as 0 so callers can skip the forward.
func hostSignalToExecSignal(sig os.Signal) int32 {
	switch sig {
	case syscall.SIGINT:
		return int32(syscall.SIGINT)
	case syscall.SIGTERM:
		return int32(syscall.SIGTERM)
	case syscall.SIGKILL:
		return int32(syscall.SIGKILL)
	default:
		return 0
	}
}

// forwardInterrupt translates SIGINT on the host into SignalExec(SIGINT) on
// the guest exec, which the agent delivers to the process group.
func forwardInterrupt(ctx context.Context, agent shellSignaler, execID string, ch <-chan os.Signal) {
	for {
		select {
		case <-ctx.Done():
			return
		case sig, ok := <-ch:
			if !ok {
				return
			}
			guestSig := hostSignalToExecSignal(sig)
			if guestSig == 0 {
				continue
			}
			if err := agent.SignalExec(ctx, execID, guestSig); err != nil {
				fmt.Fprintf(os.Stderr, "cove shell: signal: %v\r\n", err)
			}
		}
	}
}

// waitForAgentReady polls the control server's getAgent (which Pings on
// reuse) until it returns a live client or timeout elapses. Polling is
// cheap because getAgent reuses an existing connection when alive.
func waitForAgentReady(ctx context.Context, cs *ControlServer, timeout time.Duration) (*agentstate.AgentClient, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		agent, err := cs.getAgent()
		if err == nil {
			return agent, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	if lastErr == nil {
		lastErr = errors.New("timeout")
	}
	return nil, lastErr
}
