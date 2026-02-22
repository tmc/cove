// postinstall.go — Run vzscripts after VM installation.
package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// runPostInstallVZScripts boots the VM, waits for the guest agent, runs
// the comma-separated vzscript recipes, and shuts down the VM.
func runPostInstallVZScripts(recipes string) error {
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

	fmt.Printf("\n=== Post-install: running %d vzscript(s) ===\n", len(names))
	for _, n := range names {
		fmt.Printf("  - %s\n", n)
	}
	fmt.Println()

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
	}

	// Build a combined script: guest-wait, then each recipe.
	// First wait for the agent.
	fmt.Println("Waiting for VM to boot and guest agent...")
	waitScript := []byte("guest-wait 15m\n")
	if err := runVZScript(waitScript, "wait-for-agent", cfg); err != nil {
		return fmt.Errorf("waiting for agent: %w", err)
	}

	// Run each recipe in order.
	for _, name := range names {
		fmt.Printf("\n=== Running vzscript: %s ===\n", name)
		data, err := loadVZScriptData(name)
		if err != nil {
			return err
		}
		if err := runVZScript(data, name, cfg); err != nil {
			return fmt.Errorf("vzscript %s: %w", name, err)
		}
		fmt.Printf("=== Done: %s ===\n", name)
	}

	// Shut down the VM gracefully.
	fmt.Println("\nShutting down VM...")
	_, _ = ctlSendRequest(sock, &controlpb.ControlRequest{Type: "agent-shutdown"}, 30*time.Second, "agent-shutdown")

	select {
	case err := <-vmErr:
		if err != nil {
			fmt.Fprintf(os.Stderr, "VM exited with error: %v\n", err)
		}
	case <-time.After(2 * time.Minute):
		fmt.Println("VM did not exit within timeout.")
	}

	fmt.Println("\nPost-install complete.")
	return nil
}
