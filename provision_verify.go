package main

import (
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// VerifyResult holds the verification status for a single file
type VerifyResult struct {
	Path     string
	Exists   bool
	OwnerUID int
	OwnerGID int
	Mode     os.FileMode
	Expected string // expected ownership like "root:wheel"
	Status   string // "OK", "MISSING", "WRONG_OWNER"
}

// handleVerify verifies provisioning files in a VM disk
func handleVerify(args []string) error {
	fs, verboseFlag, fixFlag := newVerifyFlagSet()

	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	if *verboseFlag {
		provisionVerbose = true
	}

	// Check if VM is running.
	sock := GetControlSocketPath()
	if isVMRunning(sock) {
		return verifyRunning(sock, *verboseFlag)
	}

	return verifyStopped(*verboseFlag, *fixFlag)
}

func newVerifyFlagSet() (*flag.FlagSet, *bool, *bool) {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	verboseFlag := fs.Bool("v", false, "Verbose output")
	fixFlag := fs.Bool("fix", false, "Attempt to fix issues automatically")
	fs.Usage = func() {
		printVerifyUsage(os.Stderr, fs)
	}
	return fs, verboseFlag, fixFlag
}

func printVerifyUsage(w io.Writer, fs *flag.FlagSet) {
	fmt.Fprintf(w, `Usage: cove doctor [options]

Diagnose VM health: provisioning, agent, and file ownership.

When the VM is running, doctor checks via the control socket and guest agent.
When stopped, it mounts the disk and inspects files directly.

With --fix, doctor attempts to repair issues automatically:
  - inject a missing vz-agent binary and LaunchDaemon
  - fix file ownership (requires admin privileges)

Options:
`)
	fs.PrintDefaults()
	fmt.Fprintf(w, `
Examples:
  cove doctor
  cove doctor --fix
  cove doctor -v
`)
}

// isVMRunning checks if the VM control socket is alive.
func isVMRunning(sock string) bool {
	if _, err := os.Stat(sock); os.IsNotExist(err) {
		return false
	}
	conn, err := net.DialTimeout("unix", sock, 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// verifyRunning performs checks against a running VM via the control socket.
func verifyRunning(sock string, verbose bool) error {
	fmt.Println("=== Verifying VM (Running) ===")
	fmt.Printf("VM: %s\n", vmDir)
	fmt.Printf("Control socket: %s\n\n", sock)

	allOK := true

	// 1. Check VM status.
	req := &controlpb.ControlRequest{Type: "status"}
	resp, err := ctlSendRequest(sock, req, 5*time.Second, "status")
	if err != nil {
		fmt.Printf("  VM status: error (%v)\n", err)
		allOK = false
	} else if resp.Success {
		fmt.Printf("  VM status: OK\n")
	}

	// 2. Check agent.
	agentOK := false
	pingReq := &controlpb.ControlRequest{Type: "agent-ping"}
	pingResp, err := ctlSendRequest(sock, pingReq, 5*time.Second, "agent-ping")
	if err != nil {
		fmt.Printf("  Agent: not reachable (%v)\n", err)
	} else if !pingResp.Success {
		fmt.Printf("  Agent: not reachable (%s)\n", pingResp.Error)
	} else {
		fmt.Printf("  Agent: connected\n")
		agentOK = true
		if err := markVMAgentVerifiedForSocket(sock, vmAgentSourceVerify); err != nil && verbose {
			fmt.Printf("warning: record guest agent capability: %v\n", err)
		}
	}

	if !agentOK {
		fmt.Println()
		fmt.Println("  Agent is not running. To inject:")
		fmt.Println("    1. Stop the VM")
		fmt.Println("    2. ./cove inject -agent")
		fmt.Println("    3. ./cove run")
		allOK = false
	}

	// 3. If agent is available, check files inside guest.
	if agentOK {
		fmt.Println()
		fmt.Println("Guest file checks (via agent):")

		guestFiles := []struct {
			path string
			desc string
		}{
			{"/usr/local/bin/vz-agent", "Agent binary"},
			{"/Library/LaunchDaemons/com.github.tmc.vz-macos.vz-agent.plist", "Agent LaunchDaemon"},
			{"/private/var/db/.vz-provisioned", "Provisioning completed marker"},
			{"/private/var/db/.AppleSetupDone", "Setup Assistant skip marker"},
		}

		for _, f := range guestFiles {
			execReq := &controlpb.ControlRequest{
				Type: "agent-exec",
				Command: &controlpb.ControlRequest_AgentExec{
					AgentExec: &controlpb.AgentExecCommand{
						Args: []string{"test", "-f", f.path},
					},
				},
			}
			execResp, err := ctlSendRequest(sock, execReq, 5*time.Second, "agent-exec")
			if err == nil && execResp.Success {
				fmt.Printf("  + %s: present\n", f.desc)
			} else {
				fmt.Printf("  - %s: not found (%s)\n", f.desc, f.path)
			}
		}

		// Check vz-agent process is running.
		execReq := &controlpb.ControlRequest{
			Type: "agent-exec",
			Command: &controlpb.ControlRequest_AgentExec{
				AgentExec: &controlpb.AgentExecCommand{
					Args: []string{"pgrep", "-x", "vz-agent"},
				},
			},
		}
		execResp, err := ctlSendRequest(sock, execReq, 5*time.Second, "agent-exec")
		if err == nil && execResp.Success {
			fmt.Printf("  + vz-agent process: running\n")
		} else {
			fmt.Printf("  - vz-agent process: not running\n")
		}
	}

	reportProxyRecoveryState(os.Stdout, vmDir, &allOK)
	fmt.Println()
	if allOK {
		fmt.Println("Verification passed")
	} else {
		fmt.Println("Verification completed with issues")
	}
	return nil
}

// verifyStopped mounts the disk and inspects files directly.
// If fix is true, attempts to repair issues found.
func verifyStopped(verbose, fix bool) error {
	diskPath := filepath.Join(vmDir, "disk.img")
	if _, err := os.Stat(diskPath); os.IsNotExist(err) {
		return fmt.Errorf("disk image not found: %s\nRun 'cove install' first to create a VM", diskPath)
	}

	if err := checkDiskNotMounted(diskPath); err != nil {
		return err
	}

	fmt.Println("=== Verifying Provisioning Files (Disk) ===")
	fmt.Printf("VM: %s\n\n", vmDir)

	mountPoint, device, dataPartition, err := attachAndMountDataVolume(diskPath)
	if err != nil {
		return fmt.Errorf("mount data volume: %w", err)
	}
	defer detachDisk(device)

	// Check if provisioning already completed (self-cleaning scripts are gone).
	provisionedMarker := filepath.Join(mountPoint, "private", "var", "db", ".vz-provisioned")
	provisioned := false
	if _, statErr := os.Stat(provisionedMarker); statErr == nil {
		provisioned = true
	}

	// Provision plist/script are only required if provisioning hasn't run yet.
	provisionRequired := !provisioned

	filesToVerify := []struct {
		relativePath string
		expected     string
		required     bool
		description  string
	}{
		{"Library/LaunchDaemons/com.github.tmc.vz-macos.provision.plist", "root:wheel", provisionRequired, "LaunchDaemon plist"},
		{"private/var/db/vz-provision.sh", "root:wheel", provisionRequired, "Provisioning script"},
		{"private/var/db/.AppleSetupDone", "any", false, "Setup Assistant skip marker"},
		{"private/etc/kcpassword", "root:wheel", false, "Auto-login password (kcpassword)"},
		{"Library/Preferences/com.apple.loginwindow.plist", "root:wheel", false, "Login window preferences"},
		{autoLoginScriptRelativePath, "root:wheel", false, "Auto-login repair script"},
		{autoLoginLaunchDaemonRelativePath, "root:wheel", false, "Auto-login repair LaunchDaemon"},
		{"private/var/db/.vz-provisioned", "any", false, "Provisioning completed marker"},
		{"private/var/db/vz-guest-tools.pkg", "root:wheel", false, "SPICE guest tools package (pending install)"},
		{"private/var/db/.vz-guest-tools-installed", "any", false, "SPICE guest tools installed marker"},
		{"usr/local/bin/vz-agent", "root:wheel", false, "Guest agent binary (vz-agent)"},
		{"Library/LaunchDaemons/com.github.tmc.vz-macos.vz-agent.plist", "root:wheel", false, "Guest agent LaunchDaemon"},
	}

	var results []VerifyResult
	allOK := true
	criticalFail := false

	for _, f := range filesToVerify {
		fullPath := filepath.Join(mountPoint, f.relativePath)
		result := VerifyResult{
			Path:     f.relativePath,
			Expected: f.expected,
		}

		info, err := os.Stat(fullPath)
		if os.IsNotExist(err) {
			result.Exists = false
			if f.required {
				result.Status = "MISSING (required)"
				criticalFail = true
			} else {
				result.Status = "not present"
			}
		} else if err != nil {
			result.Status = fmt.Sprintf("error: %v", err)
			allOK = false
		} else {
			result.Exists = true
			result.Mode = info.Mode()

			if stat, ok := info.Sys().(*syscall.Stat_t); ok {
				result.OwnerUID = int(stat.Uid)
				result.OwnerGID = int(stat.Gid)

				if f.expected == "root:wheel" {
					if result.OwnerUID == 0 && result.OwnerGID == 0 {
						result.Status = "OK"
					} else {
						result.Status = fmt.Sprintf("WRONG_OWNER (uid=%d gid=%d, need root:wheel)", result.OwnerUID, result.OwnerGID)
						allOK = false
						if f.required {
							criticalFail = true
						}
					}
				} else {
					result.Status = fmt.Sprintf("OK (uid=%d gid=%d)", result.OwnerUID, result.OwnerGID)
				}
			} else {
				result.Status = "OK (ownership check unavailable)"
			}
		}

		results = append(results, result)
	}

	// Print results
	fmt.Println("File Verification Results:")
	fmt.Println(strings.Repeat("-", 80))
	for _, r := range results {
		statusIcon := "+"
		if strings.HasPrefix(r.Status, "MISSING") || strings.HasPrefix(r.Status, "WRONG_OWNER") {
			statusIcon = "!"
		} else if r.Status == "not present" {
			statusIcon = "-"
		}
		fmt.Printf("%s %s\n", statusIcon, r.Path)
		fmt.Printf("    %s: %s\n", r.Expected, r.Status)
	}
	fmt.Println(strings.Repeat("-", 80))

	// Check for missing agent.
	agentBinPath := filepath.Join(mountPoint, "usr", "local", "bin", "vz-agent")
	agentPlistPath := filepath.Join(mountPoint, "Library", "LaunchDaemons", "com.github.tmc.vz-macos.vz-agent.plist")
	agentMissing := false
	if _, err := os.Stat(agentBinPath); os.IsNotExist(err) {
		agentMissing = true
	}
	if _, err := os.Stat(agentPlistPath); os.IsNotExist(err) {
		agentMissing = true
	}

	// Collect paths needing ownership fix.
	var badOwnerPaths []string
	for _, r := range results {
		if strings.HasPrefix(r.Status, "WRONG_OWNER") {
			badOwnerPaths = append(badOwnerPaths, filepath.Join(mountPoint, r.Path))
		}
	}

	reportProxyRecoveryState(os.Stdout, vmDir, &allOK)
	fmt.Println()
	if criticalFail {
		fmt.Println("VERIFICATION FAILED: Critical files missing or have wrong ownership")
	} else if !allOK || agentMissing {
		fmt.Println("VERIFICATION WARNING: Issues found")
	} else {
		fmt.Println("VERIFICATION PASSED: All files present with correct ownership")
	}

	if provisioned {
		fmt.Println()
		fmt.Println("Note: Provisioning has already completed (found .vz-provisioned marker)")
	}

	// --fix: attempt repairs.
	if fix && (agentMissing || len(badOwnerPaths) > 0) {
		fmt.Println()
		fmt.Println("=== Applying Fixes ===")

		if agentMissing {
			fmt.Println("Injecting vz-agent...")
			// Build agent binary to temp.
			tmpBinary := filepath.Join(os.TempDir(), agentBinaryName)
			defer os.Remove(tmpBinary)
			if err := buildAgentBinary(tmpBinary); err != nil {
				fmt.Printf("  Agent build failed: %v\n", err)
			} else {
				// Write plist to temp.
				tmpPlist := filepath.Join(os.TempDir(), agentLaunchDaemonLabel+".plist")
				defer os.Remove(tmpPlist)
				os.WriteFile(tmpPlist, []byte(agentLaunchDaemonPlist), 0644)

				binDir := filepath.Join(mountPoint, "usr", "local", "bin")
				binPath := filepath.Join(binDir, agentBinaryName)
				daemonDir := filepath.Join(mountPoint, "Library", "LaunchDaemons")
				plistPath := filepath.Join(daemonDir, agentLaunchDaemonLabel+".plist")

				em := &elevatedManifest{
					RemountOwners: []string{dataPartition},
					MkdirAll:      []string{binDir, daemonDir},
					CopyFiles: []elevatedCopy{
						{Src: tmpBinary, Dst: binPath, Mode: "0755", Owner: "root:wheel"},
						{Src: tmpPlist, Dst: plistPath, Mode: "0644", Owner: "root:wheel"},
					},
				}
				if err := runElevated(em, elevationPrompt(
					fmt.Sprintf("Re-provision VM %q: fix file ownership.", elevationVMLabel()),
				)); err != nil {
					fmt.Printf("  Agent inject failed: %v\n", err)
				} else {
					fmt.Println("  Agent injected successfully")
				}
			}
		} else if len(badOwnerPaths) > 0 {
			fmt.Printf("Fixing ownership on %d file(s)...\n", len(badOwnerPaths))
			if err := fixOwnershipWithSudo(badOwnerPaths, dataPartition); err != nil {
				fmt.Printf("  Ownership fix failed: %v\n", err)
			} else {
				fmt.Println("  Ownership fixed")
			}
		}

		fmt.Println()
		fmt.Println("Fixes applied. Re-run 'doctor' to verify.")
	} else if !fix && (agentMissing || len(badOwnerPaths) > 0) {
		fmt.Println()
		fmt.Println("To fix issues automatically:")
		fmt.Println("  ./cove doctor --fix")
	}

	if criticalFail && !fix {
		return fmt.Errorf("verification failed: critical issues found\n  run 'cove doctor --fix' to attempt automatic repair")
	}
	return nil
}

func reportProxyRecoveryState(w io.Writer, vmDirectory string, allOK *bool) {
	if _, err := os.Stat(proxyStatePath(vmDirectory)); err != nil {
		return
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Warning: guest proxy recovery is pending")
	for _, line := range proxyRecoveryLines(vmDirectory) {
		fmt.Fprintf(w, "  %s\n", line)
	}
	if allOK != nil {
		*allOK = false
	}
}
