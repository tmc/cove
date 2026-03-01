// agent_inject.go - Cross-compile and inject the vz-agent binary into a VM disk.
//
// The vz-agent daemon runs inside the guest as a LaunchDaemon and exposes a
// GRPC API over vsock port 1024. The host connects via VZVirtioSocketDevice.
//
// Injection writes two files to the Data volume:
//
//   - /usr/local/bin/vz-agent (the binary)
//   - /Library/LaunchDaemons/com.github.tmc.vz-macos.vz-agent.plist (the LaunchDaemon)
//
// The LaunchDaemon is configured with KeepAlive=true so launchd restarts
// the agent if it crashes. It runs as root to allow user management and
// file operations across the guest filesystem.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	vz "github.com/tmc/apple/virtualization"
)

// agentBinaryName is the name of the guest agent binary.
const agentBinaryName = "vz-agent"

// agentLaunchDaemonLabel is the launchd label for the guest agent.
const agentLaunchDaemonLabel = "com.github.tmc.vz-macos.vz-agent"

// buildAgentBinary cross-compiles the vz-agent binary for the guest.
// It targets darwin/arm64 with CGO disabled for a static binary.
func buildAgentBinary(outputPath string) error {
	// Find the agent source directory relative to the current binary.
	// The agent source is at cmd/vz-agent/ relative to the project root.
	agentPkg := "github.com/tmc/vz-macos/cmd/vz-agent"

	cmd := exec.Command("go", "build", "-o", outputPath, agentPkg)
	cmd.Env = append(os.Environ(),
		"CGO_ENABLED=0",
		"GOOS=darwin",
		"GOARCH=arm64",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Printf("Building %s (darwin/arm64)...\n", agentBinaryName)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build agent: %w", err)
	}
	fmt.Printf("Built: %s\n", outputPath)
	return nil
}

// injectAgent cross-compiles the vz-agent binary and injects it into the
// mounted Data volume along with a LaunchDaemon plist.
//
// When running as root, files are written directly. When running as a normal
// user, directories under root-owned paths (e.g. /usr/local/bin) cannot be
// created. In that case, files are staged to temp and added to pendingInstalls
// so the caller's elevated script handles mkdir + cp + chown in one pass.
func injectAgent(mountPoint string, rootFiles *[]string, pendingInstalls *[]pendingInstall) error {
	fmt.Println()
	fmt.Println("=== Injecting Guest Agent ===")

	// Build to a temp location
	tmpBinary := filepath.Join(os.TempDir(), agentBinaryName)
	// Note: don't defer Remove — caller may need the file for the elevated script.
	// Caller is responsible for cleanup after fixOwnershipWithSudo completes.

	if err := buildAgentBinary(tmpBinary); err != nil {
		return err
	}

	binDir := filepath.Join(mountPoint, "usr", "local", "bin")
	binPath := filepath.Join(binDir, agentBinaryName)
	launchDaemonsDir := filepath.Join(mountPoint, "Library", "LaunchDaemons")
	plistPath := filepath.Join(launchDaemonsDir, agentLaunchDaemonLabel+".plist")

	// Try direct write (works when running as root).
	if err := os.MkdirAll(binDir, 0755); err != nil {
		// Can't create directory — stage for elevated install.
		fmt.Printf("Staging agent for elevated install (need root for %s)\n", binDir)

		// Stage plist to temp.
		tmpPlist := filepath.Join(os.TempDir(), agentLaunchDaemonLabel+".plist")
		if err := os.WriteFile(tmpPlist, []byte(agentLaunchDaemonPlist), 0644); err != nil {
			return fmt.Errorf("write temp agent plist: %w", err)
		}

		*pendingInstalls = append(*pendingInstalls,
			pendingInstall{Src: tmpBinary, Dest: binPath, Mode: 0755},
			pendingInstall{Src: tmpPlist, Dest: plistPath, Mode: 0644},
		)

		info, _ := os.Stat(tmpBinary)
		fmt.Printf("Staged: %s (%s, %d bytes)\n", binPath, runtime.GOARCH, info.Size())
		fmt.Printf("Staged: %s\n", plistPath)
		return nil
	}

	// Direct write succeeded — write files and record for chown.
	binaryData, err := os.ReadFile(tmpBinary)
	if err != nil {
		return fmt.Errorf("read built binary: %w", err)
	}
	os.Remove(tmpBinary)

	if err := os.WriteFile(binPath, binaryData, 0755); err != nil {
		return fmt.Errorf("write agent binary: %w", err)
	}
	chownRootWheel(binPath, rootFiles)
	fmt.Printf("Written: %s (%s, %d bytes)\n", binPath, runtime.GOARCH, len(binaryData))

	// Write the LaunchDaemon plist
	if err := os.MkdirAll(launchDaemonsDir, 0755); err != nil {
		return fmt.Errorf("create LaunchDaemons directory: %w", err)
	}

	if err := os.WriteFile(plistPath, []byte(agentLaunchDaemonPlist), 0644); err != nil {
		return fmt.Errorf("write agent plist: %w", err)
	}
	chownRootWheel(plistPath, rootFiles)
	fmt.Printf("Written: %s\n", plistPath)

	return nil
}

// injectAgentOnly mounts the VM disk and injects only the vz-agent binary
// and its LaunchDaemon plist. No user provisioning is performed.
// Uses osascript for elevated file operations so sudo is not required.
func injectAgentOnly() error {
	if err := checkVMNotRunning(); err != nil {
		return err
	}
	diskPath := filepath.Join(vmDir, "disk.img")
	if _, err := os.Stat(diskPath); os.IsNotExist(err) {
		return fmt.Errorf("disk image not found: %s\nRun 'vz-macos install' first to create a VM", diskPath)
	}
	if err := checkDiskNotMounted(diskPath); err != nil {
		return err
	}

	fmt.Println("=== Injecting Guest Agent ===")

	// Build the agent binary to a temp location first (no disk mount needed).
	tmpBinary := filepath.Join(os.TempDir(), agentBinaryName)
	defer os.Remove(tmpBinary)
	if err := buildAgentBinary(tmpBinary); err != nil {
		return err
	}

	// Write plist to temp location.
	tmpPlist := filepath.Join(os.TempDir(), agentLaunchDaemonLabel+".plist")
	defer os.Remove(tmpPlist)
	if err := os.WriteFile(tmpPlist, []byte(agentLaunchDaemonPlist), 0644); err != nil {
		return fmt.Errorf("write temp plist: %w", err)
	}

	// Mount the Data volume.
	mountPoint, device, dataPart, err := attachAndMountDataVolume(diskPath)
	if err != nil {
		return fmt.Errorf("mount data volume: %w", err)
	}
	defer detachDisk(device)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nInterrupted — detaching disk before exit...")
		detachDisk(device)
		os.Exit(1)
	}()
	defer signal.Stop(sigCh)

	binDir := filepath.Join(mountPoint, "usr", "local", "bin")
	binPath := filepath.Join(binDir, agentBinaryName)
	daemonDir := filepath.Join(mountPoint, "Library", "LaunchDaemons")
	plistPath := filepath.Join(daemonDir, agentLaunchDaemonLabel+".plist")

	// Use a single elevated shell script to enable ownership, copy files,
	// and set permissions — avoids needing sudo for the entire command.
	script := fmt.Sprintf(
		"diskutil enableOwnership %s"+
			" && mkdir -p %q %q"+
			" && cp %q %q && chmod 755 %q && chown root:wheel %q"+
			" && cp %q %q && chmod 644 %q && chown root:wheel %q",
		dataPart,
		binDir, daemonDir,
		tmpBinary, binPath, binPath, binPath,
		tmpPlist, plistPath, plistPath, plistPath,
	)

	// Write script to temp file for execution.
	tmpScript, err := os.CreateTemp("", "vz-agent-inject-*.sh")
	if err != nil {
		return fmt.Errorf("create temp script: %w", err)
	}
	tmpScriptPath := tmpScript.Name()
	defer os.Remove(tmpScriptPath)
	fmt.Fprintf(tmpScript, "#!/bin/bash\nset -e\n%s\n", script)
	tmpScript.Close()
	os.Chmod(tmpScriptPath, 0755)

	if os.Getuid() == 0 {
		fmt.Println("Running as root, copying files directly...")
		cmd := exec.Command("bash", tmpScriptPath)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("copy files: %w", err)
		}
	} else {
		fmt.Println("Requesting administrator privileges...")
		if err := runElevatedBash(tmpScriptPath); err != nil {
			return fmt.Errorf("agent inject: %w", err)
		}
	}

	info, _ := os.Stat(binPath)
	fmt.Printf("Written: %s (%s, %d bytes)\n", binPath, runtime.GOARCH, info.Size())
	fmt.Printf("Written: %s\n", plistPath)

	fmt.Println()
	fmt.Println("=== Agent Injection Complete ===")
	fmt.Println("  - vz-agent GRPC daemon installed (vsock port 1024)")
	fmt.Println("Run the VM with: ./vz-macos run")
	return nil
}

// checkAgentAvailability runs in the background after VM start. It waits
// for the guest to reach Running state, allows time for the OS to boot,
// then tries to connect to the vz-agent with retries. If the agent is
// not reachable after all attempts, it prints a hint.
func checkAgentAvailability(cs *ControlServer) {
	// Wait for the VM to reach Running state (up to 60s).
	for i := 0; i < 120; i++ {
		time.Sleep(500 * time.Millisecond)
		state := vz.VZVirtualMachineState(cs.vm.State())
		if state == vz.VZVirtualMachineStateRunning {
			break
		}
		if state == vz.VZVirtualMachineStateStopped || state == vz.VZVirtualMachineStateError {
			return // VM stopped before we could check
		}
	}

	// Allow time for the guest OS to boot and launchd to start daemons.
	time.Sleep(20 * time.Second)

	// Retry agent connection a few times (launchd may still be starting).
	for attempt := 0; attempt < 3; attempt++ {
		cs.mu.Lock()
		err := cs.ensureAgent()
		cs.mu.Unlock()
		if err == nil {
			if verbose {
				fmt.Println("Guest agent: connected")
			}
			return
		}
		time.Sleep(5 * time.Second)
	}

	fmt.Println()
	fmt.Println("Note: vz-agent is not running in this VM.")
	fmt.Println("  The agent enables remote command execution, file transfer, and SSH control.")
	fmt.Println("  To inject the agent (VM must be stopped first):")
	fmt.Println()
	fmt.Printf("    ./vz-macos inject -agent\n")
	fmt.Println()
}

// agentLaunchDaemonPlist is the launchd plist for the guest agent.
// KeepAlive ensures launchd restarts the agent if it exits.
// ThrottleInterval prevents rapid restart loops.
const agentLaunchDaemonPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.github.tmc.vz-macos.vz-agent</string>
    <key>ProgramArguments</key>
    <array>
        <string>/usr/local/bin/vz-agent</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>ThrottleInterval</key>
    <integer>5</integer>
    <key>StandardOutPath</key>
    <string>/var/log/vz-agent.log</string>
    <key>StandardErrorPath</key>
    <string>/var/log/vz-agent.log</string>
</dict>
</plist>
`
