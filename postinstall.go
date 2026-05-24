// postinstall.go — Run vzscripts after VM installation.

package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	controlpb "github.com/tmc/cove/proto/controlpb"
)

// runPostInstallVZScripts boots the VM, waits for the guest agent, runs
// the comma-separated vzscript recipes, and shuts down the VM.
func runPostInstallVZScripts(recipes string) error {
	return runPostInstallVZScriptsWithOutput(recipes, os.Stdout)
}

// runPostInstallVZScriptsWithOutput is runPostInstallVZScripts with a caller-provided
// log sink for progress and streamed command output.
func runPostInstallVZScriptsWithOutput(recipes string, out io.Writer) error {
	if out == nil {
		out = os.Stdout
	}
	names := strings.Split(recipes, ",")
	for i := range names {
		names[i] = strings.TrimSpace(names[i])
	}
	if len(names) == 0 {
		return nil
	}

	// Validate all recipes exist before booting.
	for _, name := range names {
		if _, err := loadVZScriptData(name); err != nil {
			return fmt.Errorf("recipe %q: %w", name, err)
		}
	}

	fmt.Fprintf(out, "\n=== Post-install: running %d vzscript(s) ===\n", len(names))
	for _, n := range names {
		fmt.Fprintf(out, "  - %s\n", n)
	}
	fmt.Fprintln(out)

	// Boot the VM in a goroutine.
	vmErr := make(chan error, 1)
	go func() {
		vmErr <- runMacOSVM()
	}()

	// Use the vzscript engine with guest-wait to handle boot + agent readiness.
	sock := GetControlSocketPath()
	cfg := vzscriptConfig{
		socketPath:  sock,
		execTimeout: 30 * time.Minute,
		verbose:     true,
		logWriter:   out,
		streamOut:   out,
		streamErr:   out,
	}

	// Build a combined script: guest-wait, then each recipe.
	// First wait for the agent.
	fmt.Fprintln(out, "Waiting for VM to boot and guest agent...")
	waitScript := []byte("guest-wait 15m\n")
	if err := runVZScript(waitScript, "wait-for-agent", cfg); err != nil {
		return fmt.Errorf("waiting for agent: %w", err)
	}

	// Run each recipe in order.
	for _, name := range names {
		fmt.Fprintf(out, "\n=== Running vzscript: %s ===\n", name)
		data, err := loadVZScriptData(name)
		if err != nil {
			return err
		}
		if err := runVZScript(data, name, cfg); err != nil {
			return fmt.Errorf("vzscript %s: %w", name, err)
		}
		fmt.Fprintf(out, "=== Done: %s ===\n", name)
	}

	// Shut down the VM gracefully.
	fmt.Fprintln(out, "\nShutting down VM...")
	_, _ = ctlSendRequest(sock, &controlpb.ControlRequest{Type: "agent-shutdown"}, 30*time.Second, "agent-shutdown")

	select {
	case err := <-vmErr:
		if err != nil {
			fmt.Fprintf(out, "VM exited with error: %v\n", err)
		}
	case <-time.After(2 * time.Minute):
		fmt.Fprintln(out, "VM did not exit within timeout.")
	}

	fmt.Fprintln(out, "\nPost-install complete.")
	return nil
}
