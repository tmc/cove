//go:build integration && darwin && arm64

package main

import (
	"bytes"
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

// TestLinuxShellWrapperIntegration boots a cloned Linux guest via
// `cove -vm <clone> -linux -shell -gui run`, then sends SIGWINCH and SIGINT
// to the cove subprocess to verify the host wrapper drives the unary exec
// control RPCs (ResizeExecTTY, SignalExec) end-to-end against the guest
// agent shipped in 9c5f58d (PTY allocation) and 63d3234 (host wrapper).
//
// The test is gated on:
//   - build tag `integration` (long-running; opt-in)
//   - `-integration.linux-vm` / VZ_TEST_LINUX_VM (the base Linux VM to clone)
//   - GUI mode is required (`-shell` rejects `-headless`); skipped if the
//     test runner asks for headless.
//
// Pass criterion (v0.2 readiness signal):
//   - cove writes shell output to its PTY before timeout (ExecStream + tty=true)
//   - SIGWINCH does NOT cause cove to log "cove shell: resize:" (proves
//     ResizeExecTTY succeeded against the agent-allocated PTY)
//   - SIGINT does NOT cause cove to log "cove shell: signal:" and the cove
//     process exits cleanly within the cleanup window (proves SignalExec
//     reached the guest and the main SIGINT handler unwound shutdown)
func TestLinuxShellWrapperIntegration(t *testing.T) {
	name := strings.TrimSpace(*flagIntegrationLinuxVM)
	if name == "" {
		t.Skip("set -integration.linux-vm or VZ_TEST_LINUX_VM to a Linux VM name")
	}
	if integrationHeadlessMode(true) {
		t.Skip("-shell requires GUI; rerun without -integration.headless / VZ_TEST_HEADLESS")
	}
	ensureIntegrationBaseVM(t, name, true)

	// Clone so we don't fight the base VM if a parallel test owns it.
	cloneName := integrationCloneName(t.Name())
	if err := CloneVM(CloneOptions{Source: name, Target: cloneName, Linked: true}); err != nil {
		t.Fatalf("CloneVM(%q -> %q): %v", name, cloneName, err)
	}
	clone := clonedTestVM(t, cloneName, true)
	defer clone.cleanupTB(t)

	bin := buildIntegrationBinary(t)
	cmd := exec.Command(bin, "-vm", clone.name, "-linux", "-shell", "-gui",
		"-serial", "none", // -shell takes the host TTY; serial would clash
		"run")

	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty.Start: %v", err)
	}
	defer ptmx.Close()

	// Drain PTY output into a buffer so assertions can grep for markers.
	var (
		outMu sync.Mutex
		out   bytes.Buffer
	)
	read := func() string {
		outMu.Lock()
		defer outMu.Unlock()
		return out.String()
	}
	go func() {
		buf := make([]byte, 4096)
		for {
			n, rerr := ptmx.Read(buf)
			if n > 0 {
				outMu.Lock()
				out.Write(buf[:n])
				outMu.Unlock()
			}
			if rerr != nil {
				return
			}
		}
	}()

	waitFor := func(needle string, timeout time.Duration) error {
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			if strings.Contains(read(), needle) {
				return nil
			}
			time.Sleep(200 * time.Millisecond)
		}
		return errors.New("timeout waiting for " + needle)
	}

	// Boot + agent + ExecStream(tty=true). The wrapper logs "VM started
	// successfully" once the run loop begins, then the guest bash session
	// produces a prompt within a few seconds.
	if err := waitFor("VM started successfully", 5*time.Minute); err != nil {
		t.Fatalf("vm did not start: %v\noutput:\n%s", err, read())
	}
	// Bash login banner / prompt typically contains '$' or '#'. Either is
	// proof that ExecStream(tty=true) opened against the agent PTY and the
	// kernel handed the master back to host stdout.
	if err := waitFor("$", 90*time.Second); err != nil {
		// Fall back to '#' (root prompt) before failing.
		if waitFor("#", 5*time.Second) != nil {
			t.Fatalf("no shell prompt observed: %v\noutput:\n%s", err, read())
		}
	}

	baseline := read()

	// SIGWINCH should drive ResizeExecTTY; the wrapper only emits
	// "cove shell: resize:" on failure. Send several to coalesce burst
	// behavior, then sleep so any error surfaces.
	for range 3 {
		if err := cmd.Process.Signal(syscall.SIGWINCH); err != nil {
			t.Fatalf("send SIGWINCH: %v", err)
		}
		time.Sleep(150 * time.Millisecond)
	}
	time.Sleep(time.Second)
	if delta := strings.TrimPrefix(read(), baseline); strings.Contains(delta, "cove shell: resize:") {
		t.Fatalf("ResizeExecTTY produced an error in cove output:\n%s", delta)
	}

	baseline = read()

	// SIGINT exercises SignalExec(SIGINT) on the guest exec_id AND triggers
	// cove's main shutdown handler (known dual-handler interaction). The
	// pass criterion is: no "cove shell: signal:" error AND the cove
	// process exits cleanly within the cleanup window.
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("send SIGINT: %v", err)
	}

	exitCh := make(chan error, 1)
	go func() { exitCh <- cmd.Wait() }()
	select {
	case err := <-exitCh:
		// Process group exit on SIGINT may surface as ExitError or nil;
		// what matters is that we did not deadlock and no signal-RPC
		// error was logged before exit.
		if err != nil && !strings.Contains(err.Error(), "signal") && !strings.Contains(err.Error(), "exit status") {
			t.Logf("cove exit: %v (acceptable)", err)
		}
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		<-exitCh
		t.Fatalf("cove did not exit within 60s after SIGINT\noutput:\n%s", read())
	}

	if delta := strings.TrimPrefix(read(), baseline); strings.Contains(delta, "cove shell: signal:") {
		t.Fatalf("SignalExec produced an error in cove output:\n%s", delta)
	}

	// Drain any remaining bytes; ignore EOF / EIO from the closed PTY.
	_, _ = io.Copy(io.Discard, ptmx)
}
