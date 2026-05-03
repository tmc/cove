// shell.go - Slice 2 of design 023: standalone `cove shell <vm>` client.
//
// `cove shell <vm> [-- cmd args...]` opens a Docker-shaped exec session
// against a running VM by speaking the JSON-line control-socket protocol
// to ~/.vz/vms/<vm>/control.sock. The VM-owning cove process brokers each
// frame to the in-process guest agent (Slice 1, agent_control_attach.go);
// this client owns the host TTY, signal forwarding, and stream pumping.
//
// Limitation: stdin is not yet forwarded to the guest. Slice 3 ships the
// proto bidi extension (ExecAttach) and full bidi stdin. Until then this
// matches the v0.2 `cove run -linux -shell` UX (linux_shell.go:6).
package main

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"github.com/tmc/vz-macos/internal/vmconfig"
	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// shellDefaultCommand is the guest-side command invoked when the user
// passes no `-- cmd args...` tail. Mirrors linuxShellCommand in
// linux_shell.go so `cove shell <vm>` and `cove run -linux -shell` agree.
var shellDefaultCommand = []string{"/bin/bash", "-l"}

// shellCommand is the entry point for the `cove shell` subcommand.
//
// Usage: cove shell <vm> [-- cmd args...]
//
// Returns the guest exit code on a clean exit (0 propagated as nil) so
// main.go can os.Exit(N) for non-zero results.
func shellCommand(args []string) error {
	fs := flag.NewFlagSet("shell", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { printShellUsage(os.Stderr) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	tail := fs.Args()
	if len(tail) == 0 {
		fs.Usage()
		return fmt.Errorf("vm name required")
	}
	vmArg := tail[0]
	cmd := append([]string{}, tail[1:]...)
	if len(cmd) > 0 && cmd[0] == "--" {
		cmd = cmd[1:]
	}
	if len(cmd) == 0 {
		cmd = append([]string{}, shellDefaultCommand...)
	}

	sock, err := resolveShellSocket(vmArg)
	if err != nil {
		return err
	}
	token := resolveControlTokenForSocket(sock)

	exitCode, err := runShellSession(context.Background(), sock, token, vmArg, cmd, os.Stdin, os.Stdout, os.Stderr)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		// Mirror /bin/sh: surface the exit code to the calling shell.
		os.Exit(int(exitCode))
	}
	return nil
}

// resolveShellSocket maps a VM name to its control.sock path. Returns a
// "no running VM at <name>" error if the directory does not exist —
// keeps the failure-to-dial error friendly even before we hit the socket.
func resolveShellSocket(vmName string) (string, error) {
	dir, ok := vmconfig.ExistingPath(vmName)
	if !ok {
		return "", fmt.Errorf("no running VM at %q (no such VM directory under %s)", vmName, vmconfig.BaseDir())
	}
	sock := GetControlSocketPathForVM(dir)
	if _, err := os.Stat(sock); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no running VM at %q (control socket missing at %s)\n  start it with: cove -vm %s run", vmName, sock, vmName)
		}
		return "", fmt.Errorf("stat control socket %s: %w", sock, err)
	}
	return sock, nil
}

// runShellSession is the testable session loop: dial, send attach, pump
// frames, install signal forwarders, restore TTY on exit. Returns the
// guest exit code (0 on clean exit) and an error (nil on a clean session
// even if exit code is non-zero — the exit code carries that signal).
//
// stdin is currently consulted only for term.MakeRaw and SIGWINCH sizing;
// bytes are not forwarded to the guest in Slice 2 (see file header).
func runShellSession(ctx context.Context, sock, token, vmName string, argv []string, stdin, stdout, stderr *os.File) (int32, error) {
	conn, err := net.DialTimeout("unix", sock, 10*time.Second)
	if err != nil {
		return 0, formatControlSocketDialError(sock, err)
	}
	// Long-lived connection: clear any default deadline.
	_ = conn.SetDeadline(time.Time{})
	defer conn.Close()

	attach := map[string]any{
		"type":       "agent-exec-attach",
		"args":       argv,
		"auth_token": token,
	}
	payload, err := json.Marshal(attach)
	if err != nil {
		return 0, fmt.Errorf("marshal attach: %w", err)
	}
	if _, err := conn.Write(append(payload, '\n')); err != nil {
		return 0, fmt.Errorf("send attach: %w", err)
	}

	reader := bufio.NewReaderSize(conn, 64*1024)
	first, err := reader.ReadString('\n')
	if err != nil {
		return 0, fmt.Errorf("read attach response: %w", err)
	}
	startResp := &controlpb.ControlResponse{}
	if err := protojsonUnmarshaler.Unmarshal([]byte(first), startResp); err != nil {
		return 0, fmt.Errorf("decode attach response: %w", err)
	}
	if startResp.GetError() != "" {
		return 0, mapAttachError(vmName, startResp.GetError())
	}
	var attached struct {
		Attached bool   `json:"attached"`
		ExecID   string `json:"exec_id"`
	}
	if err := json.Unmarshal([]byte(startResp.GetData()), &attached); err != nil || !attached.Attached || attached.ExecID == "" {
		return 0, fmt.Errorf("unexpected attach handshake: %s", startResp.GetData())
	}
	execID := attached.ExecID

	// Optionally enter raw mode + install signal forwarders if stdin is a
	// real terminal. When stdin is a pipe (e.g. `cove shell vm -- ls`),
	// skip raw mode and just pump output.
	stdinFD := int(stdin.Fd())
	isTTY := term.IsTerminal(stdinFD)
	var restoreTTY func()
	var restoreSignals func()
	if isTTY {
		prev, rawErr := term.MakeRaw(stdinFD)
		if rawErr != nil {
			return 0, fmt.Errorf("raw mode: %w", rawErr)
		}
		restoreTTY = func() { _ = term.Restore(stdinFD, prev) }
		// Send the initial winsize so the guest pty matches the host.
		if cols, rows, sizeErr := term.GetSize(stdinFD); sizeErr == nil {
			_ = sendShellResize(sock, token, execID, uint32(cols), uint32(rows))
		}
		restoreSignals = installShellSignalForwarders(ctx, sock, token, execID, stdinFD, stderr)
	}
	defer func() {
		if restoreSignals != nil {
			restoreSignals()
		}
		if restoreTTY != nil {
			restoreTTY()
		}
	}()

	exitCode, err := pumpShellFrames(reader, stdout, stderr)
	if err != nil {
		return exitCode, err
	}
	return exitCode, nil
}

// pumpShellFrames decodes JSON-line ControlResponse frames from r,
// writing stream chunks to stdout/stderr, and returns the guest exit code
// when a `done` frame arrives. Returns io.EOF mapped to a clean error
// only if the agent disconnected before sending `done`.
func pumpShellFrames(r *bufio.Reader, stdout, stderr io.Writer) (int32, error) {
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				return 0, fmt.Errorf("agent disconnected before exit")
			}
			return 0, fmt.Errorf("read frame: %w", err)
		}
		resp := &controlpb.ControlResponse{}
		if err := protojsonUnmarshaler.Unmarshal([]byte(line), resp); err != nil {
			return 0, fmt.Errorf("decode frame: %w", err)
		}
		if resp.GetError() != "" {
			return 0, fmt.Errorf("guest agent: %s", resp.GetError())
		}
		var frame struct {
			Stream   string `json:"stream"`
			Data     string `json:"data"`
			Done     bool   `json:"done"`
			ExitCode int32  `json:"exitCode"`
		}
		if err := json.Unmarshal([]byte(resp.GetData()), &frame); err != nil {
			// Unrecognized payloads are ignored rather than fatal so future
			// server-side additions don't break older clients.
			continue
		}
		if frame.Data != "" {
			chunk, decErr := base64.StdEncoding.DecodeString(frame.Data)
			if decErr == nil {
				dst := stdout
				if frame.Stream == "stderr" {
					dst = stderr
				}
				if _, wErr := dst.Write(chunk); wErr != nil {
					return 0, fmt.Errorf("write stream: %w", wErr)
				}
			}
		}
		if frame.Done {
			return frame.ExitCode, nil
		}
	}
}

// installShellSignalForwarders sets up SIGWINCH and SIGINT handlers that
// translate to control-socket sidecar commands. Returns a teardown
// closure callers must invoke (via defer) to stop forwarding and reclaim
// SIGINT for the main shutdown handler.
func installShellSignalForwarders(ctx context.Context, sock, token, execID string, stdinFD int, stderr io.Writer) func() {
	winchCh := make(chan os.Signal, 1)
	intCh := make(chan os.Signal, 1)
	signal.Notify(winchCh, syscall.SIGWINCH)
	// Detach the main shutdown SIGINT subscription (if any) so the user's
	// first Ctrl-C reaches only the guest. Per fb7bce2.
	signal.Reset(syscall.SIGINT)
	signal.Notify(intCh, syscall.SIGINT)

	cancelCtx, cancel := context.WithCancel(ctx)
	go forwardWinch(cancelCtx, sock, token, execID, stdinFD, winchCh, stderr)
	go forwardSignal(cancelCtx, sock, token, execID, intCh, stderr)

	return func() {
		cancel()
		signal.Stop(winchCh)
		signal.Stop(intCh)
		// Re-attach SIGINT to the main shutdown handler so post-shell
		// Ctrl-C still cleanly stops the host process / VM.
		reclaimMainSignals(syscall.SIGINT)
	}
}

func forwardWinch(ctx context.Context, sock, token, execID string, stdinFD int, ch <-chan os.Signal, stderr io.Writer) {
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-ch:
			if !ok {
				return
			}
		}
		cols, rows, err := term.GetSize(stdinFD)
		if err != nil {
			continue
		}
		if err := sendShellResize(sock, token, execID, uint32(cols), uint32(rows)); err != nil {
			fmt.Fprintf(stderr, "cove shell: resize: %v\r\n", err)
		}
	}
}

func forwardSignal(ctx context.Context, sock, token, execID string, ch <-chan os.Signal, stderr io.Writer) {
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
			if err := sendShellSignal(sock, token, execID, guestSig); err != nil {
				fmt.Fprintf(stderr, "cove shell: signal: %v\r\n", err)
			}
		}
	}
}

// sendShellResize and sendShellSignal each open a short-lived control-socket
// connection to the same socket the attach session uses and dispatch a single
// JSON-line. A fresh connection is used because the server reads one request
// per non-attach connection (see control_socket.go dispatch loop).
func sendShellResize(sock, token, execID string, cols, rows uint32) error {
	return sendShellSidecar(sock, map[string]any{
		"type": "agent-exec-resize", "auth_token": token, "exec_id": execID, "cols": cols, "rows": rows,
	})
}

func sendShellSignal(sock, token, execID string, sig int32) error {
	return sendShellSidecar(sock, map[string]any{
		"type": "agent-exec-signal", "auth_token": token, "exec_id": execID, "signal": sig,
	})
}

func sendShellSidecar(sock string, payload map[string]any) error {
	conn, err := net.DialTimeout("unix", sock, 5*time.Second)
	if err != nil {
		return formatControlSocketDialError(sock, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal sidecar: %w", err)
	}
	if _, err := conn.Write(append(body, '\n')); err != nil {
		return fmt.Errorf("send sidecar: %w", err)
	}
	// Drain the single-line response so the server's writeResponse does
	// not see EPIPE on close.
	_, _ = bufio.NewReader(conn).ReadString('\n')
	return nil
}

// mapAttachError rewrites server-side error strings into friendly,
// VM-named diagnostics for the user. The server-side strings are stable
// (see agent_control_attach.go).
func mapAttachError(vmName, raw string) error {
	low := strings.ToLower(raw)
	switch {
	case raw == "unauthorized":
		return fmt.Errorf("control token mismatch (delete %s and retry, or pass the right -token)", filepath.Join(vmconfig.BaseDir(), vmName, controlTokenFileName))
	case strings.Contains(low, "agent not ready"), strings.Contains(low, "agent unavailable"), strings.Contains(low, "no agent"):
		return fmt.Errorf("guest agent not responding (still booting?): %s", raw)
	default:
		return fmt.Errorf("attach: %s", raw)
	}
}

func printShellUsage(w io.Writer) {
	fmt.Fprintln(w, `Usage: cove shell <vm> [-- cmd args...]

Open a Docker-shaped exec session against a running VM through its
control socket. The VM-owning cove process brokers the session to the
in-guest agent over vsock.

Examples:
  cove shell my-vm                       # bash -l interactive (default)
  cove shell my-vm -- ls /tmp            # one-shot command, prints output
  cove shell my-vm -- /bin/sh -c 'echo'

Limitations (Slice 2):
  - stdin is not forwarded to the guest yet (Slice 3 / v0.3 proto bump)
  - the VM must be running with vz-agent reachable on its control socket`)
}
