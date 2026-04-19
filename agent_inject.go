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
	"strings"
	"syscall"
	"time"

	vz "github.com/tmc/apple/virtualization"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// agentBinaryName is the name of the guest agent binary.
const agentBinaryName = "vz-agent"

// agentLaunchDaemonLabel is the launchd label for the guest agent daemon (root, port 1024).
const agentLaunchDaemonLabel = "com.github.tmc.vz-macos.vz-agent"

// agentLaunchAgentLabel is the launchd label for the guest user agent (user session, port 1025).
const agentLaunchAgentLabel = "com.github.tmc.vz-macos.vz-agent-user"

// buildAgentBinary cross-compiles the vz-agent binary for the guest.
// It targets the appropriate OS/arch with CGO disabled for a static binary.
func buildAgentBinary(outputPath string) error {
	agentPkg := "github.com/tmc/vz-macos/cmd/vz-agent"

	targetOS := "darwin"
	if linuxMode {
		targetOS = "linux"
	}

	moduleDir, err := findCoveModuleDir()
	if err != nil {
		return fmt.Errorf("locate vz-macos module: %w (run cove from a checkout, or set COVE_SRC=<path-to-vz-macos>)", err)
	}

	cmd := exec.Command("go", "build", "-o", outputPath, agentPkg)
	cmd.Dir = moduleDir
	cmd.Env = append(os.Environ(),
		"CGO_ENABLED=0",
		"GOOS="+targetOS,
		"GOARCH=arm64",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Printf("Building %s (%s/arm64) from %s...\n", agentBinaryName, targetOS, moduleDir)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build agent: %w", err)
	}
	fmt.Printf("Built: %s\n", outputPath)
	return nil
}

// findCoveModuleDir locates the vz-macos module source directory so the
// guest agent can be cross-compiled from any working directory.
//
// Resolution order:
//  1. $COVE_SRC env var (explicit override)
//  2. `go list -m -f {{.Dir}} github.com/tmc/vz-macos` from the working dir
//  3. `go list -m -f {{.Dir}} github.com/tmc/vz-macos` from $GOPATH/src/github.com/tmc/vz-macos
//  4. $GOPATH/src/github.com/tmc/vz-macos if it contains go.mod
//
// Returns the directory containing go.mod, or an error if none of the above
// resolve to a valid module root.
func findCoveModuleDir() (string, error) {
	if env := os.Getenv("COVE_SRC"); env != "" {
		if _, err := os.Stat(filepath.Join(env, "go.mod")); err == nil {
			return env, nil
		}
		return "", fmt.Errorf("COVE_SRC=%s does not contain go.mod", env)
	}

	if dir, err := goListModuleDir(""); err == nil {
		return dir, nil
	}

	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		if home, err := os.UserHomeDir(); err == nil {
			gopath = filepath.Join(home, "go")
		}
	}
	if gopath != "" {
		candidate := filepath.Join(gopath, "src", "github.com", "tmc", "vz-macos")
		if _, err := os.Stat(filepath.Join(candidate, "go.mod")); err == nil {
			return candidate, nil
		}
		if dir, err := goListModuleDir(candidate); err == nil {
			return dir, nil
		}
	}

	return "", fmt.Errorf("vz-macos module not found in working dir, GOPATH, or COVE_SRC")
}

func goListModuleDir(workingDir string) (string, error) {
	cmd := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/tmc/vz-macos")
	if workingDir != "" {
		cmd.Dir = workingDir
	}
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		return "", fmt.Errorf("empty module dir")
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err != nil {
		return "", fmt.Errorf("module dir has no go.mod: %s", dir)
	}
	return dir, nil
}

// injectAgent cross-compiles the vz-agent binary and injects it into the
// mounted Data volume along with LaunchDaemon and LaunchAgent plists.
//
// When running as root, files are written directly. When running as a normal
// user, directories under root-owned paths (e.g. /usr/local/bin) cannot be
// created. In that case, files are staged to temp and added to pendingInstalls
// so the caller's elevated script handles mkdir + cp + chown in one pass.
func injectAgent(mountPoint string, rootFiles *[]string, pendingInstalls *[]pendingInstall) error {
	fmt.Println()
	fmt.Println("=== Provisioning Guest Agent ===")

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
	launchAgentsDir := filepath.Join(mountPoint, "Library", "LaunchAgents")
	daemonPlistPath := filepath.Join(launchDaemonsDir, agentLaunchDaemonLabel+".plist")
	agentPlistPath := filepath.Join(launchAgentsDir, agentLaunchAgentLabel+".plist")

	// Try direct write (works when running as root).
	if err := os.MkdirAll(binDir, 0755); err != nil {
		// Can't create directory — stage for elevated install.
		fmt.Printf("Staging agent for elevated install (need root for %s)\n", binDir)

		// Stage plists to temp.
		tmpDaemonPlist := filepath.Join(os.TempDir(), agentLaunchDaemonLabel+".plist")
		if err := os.WriteFile(tmpDaemonPlist, []byte(agentLaunchDaemonPlist), 0644); err != nil {
			return fmt.Errorf("write temp daemon plist: %w", err)
		}
		tmpAgentPlist := filepath.Join(os.TempDir(), agentLaunchAgentLabel+".plist")
		if err := os.WriteFile(tmpAgentPlist, []byte(agentLaunchAgentPlist), 0644); err != nil {
			return fmt.Errorf("write temp agent plist: %w", err)
		}

		*pendingInstalls = append(*pendingInstalls,
			pendingInstall{Src: tmpBinary, Dest: binPath, Mode: 0755},
			pendingInstall{Src: tmpDaemonPlist, Dest: daemonPlistPath, Mode: 0644},
			pendingInstall{Src: tmpAgentPlist, Dest: agentPlistPath, Mode: 0644},
		)

		info, err := os.Stat(tmpBinary)
		if err != nil {
			return fmt.Errorf("stat agent binary: %w", err)
		}
		fmt.Printf("Staged: %s (%s, %d bytes)\n", binPath, runtime.GOARCH, info.Size())
		fmt.Printf("Staged: %s\n", daemonPlistPath)
		fmt.Printf("Staged: %s\n", agentPlistPath)
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

	// Write the LaunchDaemon plist (root, port 1024).
	if err := os.MkdirAll(launchDaemonsDir, 0755); err != nil {
		return fmt.Errorf("create LaunchDaemons directory: %w", err)
	}
	if err := os.WriteFile(daemonPlistPath, []byte(agentLaunchDaemonPlist), 0644); err != nil {
		return fmt.Errorf("write daemon plist: %w", err)
	}
	chownRootWheel(daemonPlistPath, rootFiles)
	fmt.Printf("Written: %s\n", daemonPlistPath)

	// Write the LaunchAgent plist (user session, port 1025).
	if err := os.MkdirAll(launchAgentsDir, 0755); err != nil {
		return fmt.Errorf("create LaunchAgents directory: %w", err)
	}
	if err := os.WriteFile(agentPlistPath, []byte(agentLaunchAgentPlist), 0644); err != nil {
		return fmt.Errorf("write agent plist: %w", err)
	}
	chownRootWheel(agentPlistPath, rootFiles)
	fmt.Printf("Written: %s\n", agentPlistPath)

	return nil
}

// provisionAgent provisions the guest agent into the VM, choosing between
// the running-VM (vsock) path and the offline-disk (mount) path automatically.
// Idempotent: if the running VM already has the same agent version, returns
// without rebuilding.
func provisionAgent() error {
	sock := GetControlSocketPath()
	if isVMRunning(sock) {
		fmt.Println("Updating agent in running VM via vsock...")
		return provisionAgentRunning(sock)
	}
	fmt.Println("Mounting disk for offline injection...")
	return injectAgentOnly()
}

// provisionAgentRunning provisions the agent into a running VM via the
// existing vsock connection. If the agent is reachable and its version
// matches the host, returns success without rebuilding (fast path). On
// version mismatch it builds, pushes, and restarts the agent in place.
func provisionAgentRunning(sock string) error {
	pingReq := &controlpb.ControlRequest{Type: "agent-ping"}
	resp, err := ctlSendRequest(sock, pingReq, 5*time.Second, "agent-ping")
	if err != nil {
		return fmt.Errorf("agent not reachable on running vm: %w\n  the vm is running but the guest agent is not responding\n  try: cove run -gui (then log in and check /var/log/vz-agent.log)\n  or stop the vm and re-run this command for offline injection", err)
	}
	if resp.Error != "" {
		return fmt.Errorf("agent ping failed: %s", resp.Error)
	}
	guestVer := ""
	if r := resp.GetAgentPing(); r != nil {
		guestVer = r.Version
	}
	hostVer := hostVersion()
	if agentVersionsEqual(hostVer, guestVer) {
		fmt.Printf("Agent already up to date (version %s).\n", guestVer)
		if err := markVMAgentVerifiedForSocket(sock, vmAgentSourceProvision); err != nil && verbose {
			fmt.Printf("warning: record guest agent capability: %v\n", err)
		}
		return nil
	}
	fmt.Printf("Agent version %s != host %s, upgrading...\n", guestVer, hostVer)
	return upgradeAgent()
}

// agentVersionsEqual reports whether host and guest agent versions should be
// treated as equivalent for the idempotent fast path. Empty/dev/unknown
// values mean we cannot prove equivalence and must rebuild.
func agentVersionsEqual(host, guest string) bool {
	if host == "" || host == "dev" || host == "unknown" {
		return false
	}
	if guest == "" || guest == "dev" || guest == "unknown" {
		return false
	}
	return host == guest
}

// injectAgentOnly mounts the VM disk and injects the vz-agent binary,
// LaunchDaemon plist (port 1024), and LaunchAgent plist (port 1025).
// No user provisioning is performed.
func injectAgentOnly() error {
	if err := checkVMNotRunning(); err != nil {
		return err
	}
	diskPath := filepath.Join(vmDir, "disk.img")
	if _, err := os.Stat(diskPath); os.IsNotExist(err) {
		linuxDisk := filepath.Join(vmDir, "linux-disk.img")
		if _, lerr := os.Stat(linuxDisk); lerr == nil {
			return fmt.Errorf("offline agent inject is not supported for Linux VMs (found %s)\n  Linux VMs install the agent via cloud-init at install time.\n  Options:\n    - Reinstall: cove install -linux\n    - Boot the VM and install the agent over SSH manually\n    - Track the embedded-cidata fix at task #17",
				linuxDisk)
		}
		return fmt.Errorf("disk image not found: %s\nRun 'cove install' first to create a VM", diskPath)
	}
	if err := checkDiskNotMounted(diskPath); err != nil {
		return err
	}

	fmt.Println("=== Provisioning Guest Agent ===")

	// Build the agent binary to a temp location first (no disk mount needed).
	tmpBinary := filepath.Join(os.TempDir(), agentBinaryName)
	defer os.Remove(tmpBinary)
	if err := buildAgentBinary(tmpBinary); err != nil {
		return err
	}

	// Write plists to temp locations.
	tmpDaemonPlist := filepath.Join(os.TempDir(), agentLaunchDaemonLabel+".plist")
	defer os.Remove(tmpDaemonPlist)
	if err := os.WriteFile(tmpDaemonPlist, []byte(agentLaunchDaemonPlist), 0644); err != nil {
		return fmt.Errorf("write temp daemon plist: %w", err)
	}
	tmpAgentPlist := filepath.Join(os.TempDir(), agentLaunchAgentLabel+".plist")
	defer os.Remove(tmpAgentPlist)
	if err := os.WriteFile(tmpAgentPlist, []byte(agentLaunchAgentPlist), 0644); err != nil {
		return fmt.Errorf("write temp agent plist: %w", err)
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
	agentDir := filepath.Join(mountPoint, "Library", "LaunchAgents")
	daemonPlistPath := filepath.Join(daemonDir, agentLaunchDaemonLabel+".plist")
	agentPlistPath := filepath.Join(agentDir, agentLaunchAgentLabel+".plist")

	// Use a single elevated shell script to enable ownership, copy files,
	// and set permissions — avoids needing sudo for the entire command.
	script := fmt.Sprintf(
		"diskutil enableOwnership %s"+
			" && mkdir -p %q %q %q"+
			" && cp %q %q && chmod 755 %q && chown root:wheel %q"+
			" && cp %q %q && chmod 644 %q && chown root:wheel %q"+
			" && cp %q %q && chmod 644 %q && chown root:wheel %q",
		dataPart,
		binDir, daemonDir, agentDir,
		tmpBinary, binPath, binPath, binPath,
		tmpDaemonPlist, daemonPlistPath, daemonPlistPath, daemonPlistPath,
		tmpAgentPlist, agentPlistPath, agentPlistPath, agentPlistPath,
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
		fmt.Println()
		fmt.Println("Administrator privileges required to inject the guest agent.")
		fmt.Println()
		fmt.Println("  What this will do:")
		fmt.Printf("    - Copy vz-agent binary to %s (owner: root:wheel)\n", binPath)
		fmt.Printf("    - Write LaunchDaemon plist to %s (owner: root:wheel)\n", daemonPlistPath)
		fmt.Printf("    - Write user agent plist to %s\n", agentPlistPath)
		fmt.Println()
		if err := runElevatedBash(tmpScriptPath, "cove will copy the guest agent binary and LaunchDaemon plists into the VM disk image with root:wheel ownership."); err != nil {
			return fmt.Errorf("agent inject: %w", err)
		}
	}

	info, err := os.Stat(binPath)
	if err != nil {
		return fmt.Errorf("stat installed agent binary: %w", err)
	}
	fmt.Printf("Written: %s (%s, %d bytes)\n", binPath, runtime.GOARCH, info.Size())
	fmt.Printf("Written: %s\n", daemonPlistPath)
	fmt.Printf("Written: %s\n", agentPlistPath)

	fmt.Println()
	fmt.Println("=== Agent Provisioning Complete ===")
	fmt.Println("  - vz-agent daemon installed (vsock port 1024, root)")
	fmt.Println("  - vz-agent user agent installed (vsock port 1025, user session)")
	fmt.Println("Run the VM with: ./cove run")
	if err := setVMAgentRequested(vmDir, detectVMAgentPlatform(vmDir), true, vmAgentSourceProvision); err != nil {
		fmt.Printf("warning: save guest agent config: %v\n", err)
	}
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
		_, err := cs.getAgent()
		if err == nil {
			if err := markVMAgentVerified(cs.effectiveVMDir(), currentVMAgentPlatform(), vmAgentSourceRuntime, time.Now()); err != nil && verbose {
				fmt.Printf("warning: record guest agent capability: %v\n", err)
			}
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
	fmt.Println()
	exe := "./cove"
	vmFlag := ""
	if vmName != "" && vmName != "default" {
		vmFlag = " -vm " + vmName
	}
	fmt.Println("  To fix, stop the VM and re-provision:")
	fmt.Println()
	fmt.Printf("    %s%s provision -user <username> -skip-setup-assistant\n", exe, vmFlag)
	fmt.Println()
	fmt.Println("  Or to provision just the agent:")
	fmt.Println()
	fmt.Printf("    %s%s provision-agent\n", exe, vmFlag)
	fmt.Println()
}

// upgradeAgent builds a new vz-agent binary and deploys it to a running VM
// via the control socket. It copies the binary, restarts the service, and
// verifies the new version is running.
func upgradeAgent() error {
	sock := GetControlSocketPath()

	// Check current agent version.
	pingReq := &controlpb.ControlRequest{Type: "agent-ping"}
	resp, err := ctlSendRequest(sock, pingReq, 5*time.Second, "agent-ping")
	if err != nil {
		return fmt.Errorf("agent not reachable (is the VM running?): %w", err)
	}
	oldVersion := ""
	if r := resp.GetAgentPing(); r != nil {
		oldVersion = r.Version
	}
	fmt.Printf("Current agent version: %s\n", oldVersion)

	// Build new agent binary.
	tmpBinary := filepath.Join(os.TempDir(), agentBinaryName)
	defer os.Remove(tmpBinary)
	if err := buildAgentBinary(tmpBinary); err != nil {
		return err
	}

	// Copy to guest via agent-cp.
	absPath, _ := filepath.Abs(tmpBinary)
	fmt.Println("Copying agent to guest...")
	cpReq := &controlpb.ControlRequest{
		Type: "agent-cp",
		Command: &controlpb.ControlRequest_AgentCp{
			AgentCp: &controlpb.AgentCopyCommand{
				HostPath:  absPath,
				GuestPath: "/usr/local/bin/" + agentBinaryName,
				ToGuest:   true,
				Mode:      0755,
			},
		},
	}
	resp, err = ctlSendRequest(sock, cpReq, 2*time.Minute, "agent-cp")
	if err != nil {
		return fmt.Errorf("copy agent: %w", err)
	}
	if resp.Error != "" {
		return fmt.Errorf("copy agent: %s", resp.Error)
	}
	fmt.Println("Copied.")

	// Restart the agent service via launchctl kickstart.
	fmt.Println("Restarting agent service...")
	execReq := &controlpb.ControlRequest{
		Type: "agent-exec",
		Command: &controlpb.ControlRequest_AgentExec{
			AgentExec: &controlpb.AgentExecCommand{
				Args: []string{"launchctl", "kickstart", "-k", "system/" + agentLaunchDaemonLabel},
			},
		},
	}
	// The kickstart kills the agent, so the connection may drop — that's expected.
	ctlSendRequest(sock, execReq, 10*time.Second, "agent-exec")

	// Wait for launchd to restart the agent with KeepAlive.
	fmt.Println("Waiting for new agent...")
	time.Sleep(5 * time.Second)

	// Reconnect: the old vsock connection is dead, force a new one.
	connectReq := &controlpb.ControlRequest{Type: "agent-connect"}
	var connected bool
	for attempt := 0; attempt < 10; attempt++ {
		resp, err = ctlSendRequest(sock, connectReq, 10*time.Second, "agent-connect")
		if err == nil && resp.Error == "" {
			connected = true
			break
		}
		fmt.Printf("  reconnect attempt %d...\n", attempt+1)
		time.Sleep(3 * time.Second)
	}
	if !connected {
		return fmt.Errorf("agent did not come back after upgrade (tried 10 reconnects)")
	}

	resp, err = ctlSendRequest(sock, pingReq, 10*time.Second, "agent-ping")
	if err != nil {
		return fmt.Errorf("agent ping failed after upgrade: %w", err)
	}
	if resp.Error != "" {
		return fmt.Errorf("agent ping failed: %s", resp.Error)
	}
	newVersion := ""
	if r := resp.GetAgentPing(); r != nil {
		newVersion = r.Version
	}
	if err := markVMAgentVerifiedForSocket(sock, vmAgentSourceUpgrade); err != nil && verbose {
		fmt.Printf("warning: record guest agent capability: %v\n", err)
	}
	fmt.Printf("Upgraded: %s → %s\n", oldVersion, newVersion)
	return nil
}

// agentLaunchDaemonPlist is the launchd plist for the guest agent daemon.
// Runs as root on boot (port 1024). KeepAlive ensures launchd restarts if it crashes.
// Loaded from templates/com.github.tmc.vz-macos.vz-agent.plist via go:embed.
var agentLaunchDaemonPlist = agentLaunchDaemonPlistEmbed

// agentLaunchAgentPlist is the launchd plist for the guest user agent.
// Runs in the logged-in user's session (port 1025) with TCC/FDA grants.
// LimitLoadToSessionType: Aqua ensures it only starts in GUI sessions.
// Loaded from templates/com.github.tmc.vz-macos.vz-agent-user.plist via go:embed.
var agentLaunchAgentPlist = agentLaunchAgentPlistEmbed
