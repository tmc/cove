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
	"flag"
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

	agentstate "github.com/tmc/vz-macos/internal/agent"
	controlpb "github.com/tmc/vz-macos/proto/controlpb"
)

// agentBinaryName is the name of the guest agent binary.
const agentBinaryName = "vz-agent"

// agentLaunchDaemonLabel is the launchd label for the guest agent daemon (root, port 1024).
const agentLaunchDaemonLabel = "com.github.tmc.vz-macos.vz-agent"

// agentLaunchAgentLabel is the launchd label for the guest user agent (user session, port 1025).
const agentLaunchAgentLabel = "com.github.tmc.vz-macos.vz-agent-user"

const (
	agentUpgradeReconnectInitialDelay = 5 * time.Second
	agentUpgradeReconnectAttempts     = 30
	agentUpgradeReconnectDelay        = 3 * time.Second
	agentUpgradeReconnectTimeout      = 10 * time.Second
)

// buildAgentBinary cross-compiles the vz-agent binary for the guest.
// It targets the appropriate OS/arch with CGO disabled for a static binary.
func buildAgentBinary(outputPath string) error {
	targetOS := "darwin"
	if linuxMode {
		targetOS = "linux"
	}
	return buildAgentBinaryForOS(outputPath, targetOS)
}

func buildAgentBinaryForOS(outputPath, targetOS string) error {
	agentPkg := "github.com/tmc/vz-macos/cmd/vz-agent"

	moduleDir, err := findCoveModuleDir()
	if err != nil {
		return fmt.Errorf("locate vz-macos module: %w (run cove from a checkout, or set COVE_SRC=<path-to-vz-macos>)", err)
	}

	cmd := exec.Command("go", "build", "-buildvcs=false", "-ldflags", agentBuildLDFlags(), "-o", outputPath, agentPkg)
	cmd.Dir = moduleDir
	cmd.Env = append(os.Environ(),
		"CGO_ENABLED=0",
		"GOOS="+targetOS,
		"GOARCH=arm64",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if verbose {
		fmt.Printf("Building %s (%s/arm64) from %s...\n", agentBinaryName, targetOS, moduleDir)
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build agent: %w", err)
	}
	if verbose {
		fmt.Printf("Built: %s\n", outputPath)
	}

	if targetOS == "darwin" {
		if err := codesignAdHoc(outputPath); err != nil {
			return fmt.Errorf("codesign agent binary: %w", err)
		}
	}
	return nil
}

func agentBuildLDFlags() string {
	info := resolvedVersion()
	return fmt.Sprintf("-X main.version=%s -X main.commit=%s -X main.date=%s", hostVersion(), info.Commit, info.Date)
}

// codesignAdHoc applies an ad-hoc signature to a Mach-O binary. macOS
// Sequoia and later kill unsigned binaries at exec time via amfid, so
// the guest agent must be signed before being copied into the VM disk.
func codesignAdHoc(path string) error {
	cmd := exec.Command("codesign", "-s", "-", "-f", path)
	if verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Run(); err != nil {
		return err
	}
	if verbose {
		fmt.Printf("Signed: %s (ad-hoc)\n", path)
	}
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

// provisionAgent provisions the guest agent into the VM, choosing between
// the running-VM (vsock) path and the offline-disk (mount) path automatically.
// Idempotent: if the running VM already has the same agent version, returns
// without rebuilding.
func provisionAgent() error {
	target := currentVMSelection()
	if vmName != "" {
		var err error
		target, err = requireExistingVMSelection("provision-agent", vmName)
		if err != nil {
			return err
		}
	}
	return provisionAgentForVM(target)
}

func provisionAgentForVM(target vmSelection) error {
	sock := target.controlSocketPath()
	if isVMRunning(sock) {
		fmt.Println("Updating agent in running VM via vsock...")
		return provisionAgentRunning(sock)
	}
	fmt.Println("Mounting disk for offline injection...")
	return injectAgentOnlyForVM(target)
}

func handleAgentUpgradeCommand(args []string) error {
	fs := flag.NewFlagSet("agent-upgrade", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	vmFlag := fs.String("vm", "", "VM name")
	fs.Usage = func() {
		printAgentUpgradeUsage(os.Stderr)
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("usage: cove agent-upgrade [-vm <name>]")
	}
	target := currentVMSelection()
	name := vmName
	if *vmFlag != "" {
		name = *vmFlag
	}
	if name != "" {
		var err error
		target, err = requireExistingVMSelection("agent-upgrade", name)
		if err != nil {
			return err
		}
	}
	return upgradeAgentAt(target.controlSocketPath())
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
	if agentstate.VersionsEqual(hostVer, guestVer) {
		fmt.Printf("Agent already up to date (version %s).\n", guestVer)
		if err := agentstate.MarkVerifiedForSocket(sock, agentstate.SourceProvision); err != nil && verbose {
			fmt.Printf("warning: record guest agent capability: %v\n", err)
		}
		return nil
	}
	fmt.Printf("Agent version %s != host %s, upgrading...\n", guestVer, hostVer)
	return upgradeAgentAt(sock)
}

// injectAgentOnlyForVM mounts the VM disk and injects the vz-agent binary,
// LaunchDaemon plist (port 1024), and LaunchAgent plist (port 1025).
// No user provisioning is performed.
func injectAgentOnlyForVM(target vmSelection) error {
	if err := checkVMNotRunningAt(target.Directory); err != nil {
		return err
	}
	diskPath := target.diskPath()
	if _, err := os.Stat(diskPath); os.IsNotExist(err) {
		linuxDisk := target.linuxDiskPath()
		if _, lerr := os.Stat(linuxDisk); lerr == nil {
			return fmt.Errorf("offline agent inject is not supported for Linux VMs (found %s)\n  Linux VMs install the agent via cloud-init at install time.\n  Options:\n    - Reinstall: cove install -linux\n    - Boot the VM and install the agent over SSH manually\n    - Track the embedded-cidata fix at task #17",
				linuxDisk)
		}
		return fmt.Errorf("disk image not found: %s\nRun 'cove install' first to create a VM", diskPath)
	}

	// In restricted environments (Claude Code, sandboxed shell), we cannot
	// show the macOS authorization dialog, hold a disk mount across user
	// action, or guarantee temp files survive cleanup defers. Build the
	// agent and a self-contained installer script into a stable location
	// the user can re-run by hand, then bail with clear instructions. The
	// generated script does its own mount + copy + detach.
	if restrictedEnvironment() && os.Getuid() != 0 && !helperInstalled() {
		return injectAgentOnlyRestricted(target, diskPath)
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
	defer detachDiskForPath(device, diskPath)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nInterrupted — detaching disk before exit...")
		detachDiskForPath(device, diskPath)
		os.Exit(1)
	}()
	defer signal.Stop(sigCh)

	binDir := filepath.Join(mountPoint, "usr", "local", "bin")
	binPath := filepath.Join(binDir, agentBinaryName)
	daemonDir := filepath.Join(mountPoint, "Library", "LaunchDaemons")
	agentDir := filepath.Join(mountPoint, "Library", "LaunchAgents")
	daemonPlistPath := filepath.Join(daemonDir, agentLaunchDaemonLabel+".plist")
	agentPlistPath := filepath.Join(agentDir, agentLaunchAgentLabel+".plist")

	if os.Getuid() != 0 {
		fmt.Println()
		fmt.Println("Administrator privileges required to inject the guest agent.")
		fmt.Println()
		fmt.Println("  What this will do:")
		fmt.Printf("    - Copy vz-agent binary to %s (owner: root:wheel)\n", binPath)
		fmt.Printf("    - Write LaunchDaemon plist to %s (owner: root:wheel)\n", daemonPlistPath)
		fmt.Printf("    - Write user agent plist to %s\n", agentPlistPath)
		fmt.Println()
	}

	em := &elevatedManifest{
		RemountOwners: []string{dataPart},
		MkdirAll:      []string{binDir, daemonDir, agentDir},
		CopyFiles: []elevatedCopy{
			{Src: tmpBinary, Dst: binPath, Mode: "0755", Owner: "root:wheel"},
			{Src: tmpDaemonPlist, Dst: daemonPlistPath, Mode: "0644", Owner: "root:wheel"},
			{Src: tmpAgentPlist, Dst: agentPlistPath, Mode: "0644", Owner: "root:wheel"},
		},
	}
	if err := runElevated(em, elevationPrompt(
		fmt.Sprintf("Install guest agent into VM %q.", target.elevationLabel()),
	)); err != nil {
		return fmt.Errorf("agent inject: %w", err)
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
	fmt.Printf("Run the VM with: ./cove%s run\n", target.hintFlag())
	if err := agentstate.SetRequested(target.Directory, agentstate.DetectPlatform(target.Directory), true, agentstate.SourceProvision); err != nil {
		fmt.Printf("warning: save guest agent config: %v\n", err)
	}
	return nil
}

// injectAgentOnlyRestricted handles agent injection when cove is running in
// a restricted environment (sandboxed shell, no controlling tty for password
// dialog). It builds the agent and writes a self-contained installer script
// into ~/.vz/pending-elevation/<timestamp>/ that the user can re-run by hand
// in a real terminal. The script does its own mount + copy + detach.
func injectAgentOnlyRestricted(target vmSelection, diskPath string) error {
	fmt.Println("=== Provisioning Guest Agent (restricted environment) ===")
	fmt.Println("Detected sandboxed environment; preparing self-contained installer.")

	stagingRoot := filepath.Join(os.Getenv("HOME"), ".vz", "pending-elevation")
	if err := os.MkdirAll(stagingRoot, 0755); err != nil {
		return fmt.Errorf("create staging root: %w", err)
	}
	staging, err := os.MkdirTemp(stagingRoot, "agent-")
	if err != nil {
		return fmt.Errorf("create staging dir: %w", err)
	}

	binPath := filepath.Join(staging, agentBinaryName)
	if err := buildAgentBinary(binPath); err != nil {
		return err
	}

	daemonPlistPath := filepath.Join(staging, agentLaunchDaemonLabel+".plist")
	if err := os.WriteFile(daemonPlistPath, []byte(agentLaunchDaemonPlist), 0644); err != nil {
		return fmt.Errorf("write daemon plist: %w", err)
	}
	agentPlistPath := filepath.Join(staging, agentLaunchAgentLabel+".plist")
	if err := os.WriteFile(agentPlistPath, []byte(agentLaunchAgentPlist), 0644); err != nil {
		return fmt.Errorf("write agent plist: %w", err)
	}

	scriptPath := filepath.Join(staging, "install.sh")
	script := fmt.Sprintf(`#!/bin/bash
# cove agent installer — generated for %s
# Run this with sudo. It mounts the VM disk, copies the agent and plists
# with root:wheel ownership, then detaches the disk.
set -euo pipefail

DISK=%q
STAGE=%q

if [ "$(id -u)" != "0" ]; then
  echo "must run as root: sudo bash $0" >&2
  exit 1
fi

# Pre-flight: another process (usually a running cove VM) holding the disk
# will make hdiutil attach fail with a cryptic "Resource temporarily
# unavailable". Name the holder so the user knows what to stop.
HOLDERS=$({ /usr/sbin/lsof -- "$DISK" 2>/dev/null || true; } | /usr/bin/awk 'NR>1 {print $2":"$1}' | sort -u)
if [ -n "$HOLDERS" ]; then
  echo "cannot attach VM disk for offline agent injection: $DISK" >&2
  echo "disk is open by another process:" >&2
  for h in $HOLDERS; do
    pid=${h%%:*}
    cmd=${h#*:}
    elapsed=$(/bin/ps -o etime= -p "$pid" 2>/dev/null | tr -d ' ')
    if [ -n "$elapsed" ]; then
      echo "  pid=$pid cmd=$cmd elapsed=$elapsed" >&2
    else
      echo "  pid=$pid cmd=$cmd" >&2
    fi
  done
  echo >&2
  echo "stop the VM (or kill the cove process) and re-run this script." >&2
  exit 1
fi

# Pre-flight: check for a stale hdiutil attach from a crashed cove. If the
# disk is already attached we cannot safely continue, but detaching might
# clobber live state — tell the user and let them decide.
STALE_NODE=$(/usr/bin/hdiutil info 2>/dev/null | /usr/bin/awk -v d="$DISK" '
  $0 ~ /^image-path/ { path=$0; sub(/^image-path[ \t]*:[ \t]*/, "", path) }
  $1 == "/dev/disk" && path == d { print $1; exit }
  /^\/dev\/disk[0-9]+/ && path == d { print $1; exit }
')
if [ -n "$STALE_NODE" ]; then
  echo "cannot attach VM disk for offline agent injection: $DISK" >&2
  echo "a stale hdiutil attach already exists at $STALE_NODE" >&2
  echo "(probably from a previous crashed cove). If nothing is using it, detach with:" >&2
  echo "  sudo hdiutil detach $STALE_NODE" >&2
  echo "then re-run this script." >&2
  exit 1
fi

echo "==> Attaching disk: $DISK"
ATTACH_OUT=$(hdiutil attach "$DISK" -nobrowse -noverify -noautoopen -plist 2>&1) || {
  echo "attach VM disk for offline agent injection failed ($DISK):" >&2
  echo "$ATTACH_OUT" >&2
  # Re-scan for a stale attach that appeared between pre-flight and attach.
  STALE_NODE=$(/usr/bin/hdiutil info 2>/dev/null | /usr/bin/awk -v d="$DISK" '
    $0 ~ /^image-path/ { path=$0; sub(/^image-path[ \t]*:[ \t]*/, "", path) }
    /^\/dev\/disk[0-9]+/ && path == d { print $1; exit }
  ')
  if [ -n "$STALE_NODE" ]; then
    echo >&2
    echo "stale attach detected at $STALE_NODE — detach with:" >&2
    echo "  sudo hdiutil detach $STALE_NODE" >&2
  fi
  exit 1
}

DEVICE=$(echo "$ATTACH_OUT" | /usr/bin/plutil -convert xml1 -o - - | \
  /usr/bin/awk '/<key>dev-entry<\/key>/{getline; gsub(/.*<string>|<\/string>.*/,""); print; exit}')
if [ -z "${DEVICE:-}" ]; then
  echo "could not determine device node from attach output" >&2
  echo "$ATTACH_OUT" >&2
  exit 1
fi
echo "==> Container device: $DEVICE"

DATA_PART=""
for p in $(diskutil list "$DEVICE" | awk '/Data/ && /APFS Volume/ {print $NF}'); do
  if diskutil info "$p" 2>/dev/null | grep -q "Volume Name:.*Data"; then
    DATA_PART="$p"
    break
  fi
done
if [ -z "$DATA_PART" ]; then
  DATA_PART=$(diskutil list "$DEVICE" | awk '/APFS Volume.*Data/ {print $NF; exit}')
fi
if [ -z "$DATA_PART" ]; then
  echo "could not find Data volume in $DEVICE" >&2
  diskutil list "$DEVICE" >&2
  hdiutil detach "$DEVICE" || true
  exit 1
fi
echo "==> Data partition: /dev/$DATA_PART"

diskutil mount /dev/"$DATA_PART" >/dev/null
MOUNT=$(diskutil info /dev/"$DATA_PART" | awk -F: '/Mount Point/ {gsub(/^ +/,"",$2); print $2; exit}')
if [ -z "$MOUNT" ]; then
  echo "could not determine mount point for /dev/$DATA_PART" >&2
  hdiutil detach "$DEVICE" || true
  exit 1
fi
echo "==> Mount point: $MOUNT"

cleanup() {
  echo "==> Detaching $DEVICE"
  hdiutil detach "$DEVICE" || diskutil unmountDisk force "$DEVICE" || true
}
trap cleanup EXIT

diskutil enableOwnership /dev/"$DATA_PART"
mount -uo owners /dev/"$DATA_PART"

BIN_DIR="$MOUNT/usr/local/bin"
DAEMON_DIR="$MOUNT/Library/LaunchDaemons"
AGENT_DIR="$MOUNT/Library/LaunchAgents"
mkdir -p "$BIN_DIR" "$DAEMON_DIR" "$AGENT_DIR"

cp "$STAGE/%s" "$BIN_DIR/%s"
chmod 755 "$BIN_DIR/%s"
chown root:wheel "$BIN_DIR/%s"
echo "    wrote $BIN_DIR/%s"

cp "$STAGE/%s" "$DAEMON_DIR/%s"
chmod 644 "$DAEMON_DIR/%s"
chown root:wheel "$DAEMON_DIR/%s"
echo "    wrote $DAEMON_DIR/%s"

cp "$STAGE/%s" "$AGENT_DIR/%s"
chmod 644 "$AGENT_DIR/%s"
chown root:wheel "$AGENT_DIR/%s"
echo "    wrote $AGENT_DIR/%s"

echo
echo "=== Agent injection complete ==="
echo "Boot the VM with: cove%s run"
`,
		target.elevationLabel(),
		diskPath,
		staging,
		agentBinaryName, agentBinaryName, agentBinaryName, agentBinaryName, agentBinaryName,
		agentLaunchDaemonLabel+".plist", agentLaunchDaemonLabel+".plist",
		agentLaunchDaemonLabel+".plist", agentLaunchDaemonLabel+".plist",
		agentLaunchDaemonLabel+".plist",
		agentLaunchAgentLabel+".plist", agentLaunchAgentLabel+".plist",
		agentLaunchAgentLabel+".plist", agentLaunchAgentLabel+".plist",
		agentLaunchAgentLabel+".plist",
		target.hintFlag(),
	)
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		return fmt.Errorf("write installer script: %w", err)
	}

	if err := agentstate.SetRequested(target.Directory, agentstate.DetectPlatform(target.Directory), true, agentstate.SourceProvision); err != nil && verbose {
		fmt.Printf("warning: save guest agent config: %v\n", err)
	}

	fmt.Println()
	fmt.Println("Cannot complete injection here (no terminal for password dialog).")
	fmt.Println("All build steps done; run this in a real terminal to finish:")
	fmt.Println()
	fmt.Printf("  sudo bash %s\n", scriptPath)
	fmt.Println()
	fmt.Println("The script is self-contained: it mounts the disk, copies the signed")
	fmt.Println("agent + plists with root:wheel ownership, then detaches the disk.")
	fmt.Println()
	fmt.Printf("Staging directory (safe to delete after success): %s\n", staging)
	fmt.Println()
	fmt.Println("To skip this step in the future, install the cove helper once:")
	fmt.Println("  sudo cove helper install")
	return errRestrictedNoElevation
}

// checkAgentAvailability runs in the background after VM start. It waits
// for the guest to reach Running state, allows time for the OS to boot,
// then tries to connect to the vz-agent with retries. If the agent is
// not reachable after all attempts, it prints a hint.
func checkAgentAvailability(target runtimeAgentAvailabilityTarget) {
	if target == nil {
		return
	}

	// Wait for the VM to reach Running state (up to 60s).
	for i := 0; i < 120; i++ {
		time.Sleep(500 * time.Millisecond)
		state, err := target.currentVMState()
		if err != nil {
			return
		}
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
		_, err := target.getAgent()
		if err == nil {
			vmDirectory := target.effectiveVMDir()
			if err := agentstate.MarkVerified(vmDirectory, agentstate.DetectPlatform(vmDirectory), agentstate.SourceRuntime, time.Now()); err != nil && verbose {
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
	vmFlag := target.vmHintFlag()
	vmDirectory := target.effectiveVMDir()
	if agentstate.DetectPlatform(vmDirectory) == agentstate.PlatformMacOS {
		fmt.Println("  To fix, stop the VM and re-provision:")
		fmt.Println()
		fmt.Printf("    %s%s provision -user <username> -skip-setup-assistant\n", exe, vmFlag)
		fmt.Println()
		fmt.Println("  Or to provision just the agent:")
	} else {
		fmt.Println("  To fix, stop the VM and provision the agent:")
	}
	fmt.Println()
	fmt.Printf("    %s%s provision-agent\n", exe, vmFlag)
	fmt.Println()
}

// upgradeAgent builds a new vz-agent binary and deploys it to a running VM
// via the control socket. It copies the binary, restarts the service, and
// verifies the new version is running.
func upgradeAgent() error {
	return upgradeAgentAt(currentVMSelection().controlSocketPath())
}

func upgradeAgentAt(sock string) error {
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
	guestOS := ""
	infoReq := &controlpb.ControlRequest{Type: "agent-info"}
	if infoResp, err := ctlSendRequest(sock, infoReq, 10*time.Second, "agent-info"); err == nil && infoResp.Error == "" {
		if info := infoResp.GetAgentInfo(); info != nil {
			guestOS = info.GetOs()
		}
	}
	targetOS := agentBuildTargetOS(guestOS)

	// Build new agent binary.
	tmpBinary := filepath.Join(os.TempDir(), agentBinaryName)
	defer os.Remove(tmpBinary)
	if err := buildAgentBinaryForOS(tmpBinary, targetOS); err != nil {
		return err
	}

	// Copy to guest via agent-cp.
	absPath, _ := filepath.Abs(tmpBinary)
	guestTmpPath := fmt.Sprintf("/tmp/%s-upgrade-%d", agentBinaryName, time.Now().UnixNano())
	fmt.Println("Copying agent to guest...")
	cpReq := &controlpb.ControlRequest{
		Type: "agent-cp",
		Command: &controlpb.ControlRequest_AgentCp{
			AgentCp: &controlpb.AgentCopyCommand{
				HostPath:  absPath,
				GuestPath: guestTmpPath,
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

	installReq := &controlpb.ControlRequest{
		Type: "agent-exec",
		Command: &controlpb.ControlRequest_AgentExec{
			AgentExec: &controlpb.AgentExecCommand{
				Args: []string{"sh", "-c", guestAgentUpgradeInstallScript(guestTmpPath, "/usr/local/bin/"+agentBinaryName)},
			},
		},
	}
	resp, err = ctlSendRequest(sock, installReq, 30*time.Second, "agent-exec")
	if err != nil {
		return fmt.Errorf("install agent: %w", err)
	}
	if resp.Error != "" {
		return fmt.Errorf("install agent: %s", resp.Error)
	}
	if r := resp.GetAgentExecResult(); r != nil && r.ExitCode != 0 {
		return fmt.Errorf("install agent: %s", strings.TrimSpace(r.Stderr))
	}
	fmt.Println("Installed.")

	if isLinuxGuestOS(guestOS) {
		fmt.Println("Restarting agent daemon (port 1024)...")
		execReq := &controlpb.ControlRequest{
			Type: "agent-exec",
			Command: &controlpb.ControlRequest_AgentExec{
				AgentExec: &controlpb.AgentExecCommand{
					Args: linuxAgentRestartCommand(),
				},
			},
		}
		ctlSendRequest(sock, execReq, 10*time.Second, "agent-exec")
	} else {
		// Restart the user agent (port 1025) before the daemon — once we kickstart
		// the daemon below, the connection drops and we lose the chance to send
		// further commands. The bounce script reads the GUI user's uid from the
		// console owner so it works even when no specific user was provisioned.
		// Both labels are bounced via launchctl kickstart -k.
		fmt.Println("Restarting user agent service (port 1025)...")
		bounceUserAgent := &controlpb.ControlRequest{
			Type: "agent-exec",
			Command: &controlpb.ControlRequest_AgentExec{
				AgentExec: &controlpb.AgentExecCommand{
					Args: []string{
						"sh", "-c",
						fmt.Sprintf(`uid=$(stat -f %%u /dev/console); `+
							`if [ -n "$uid" ] && [ "$uid" != "0" ]; then `+
							`  launchctl asuser "$uid" launchctl kickstart -k "gui/$uid/%s" 2>/dev/null || true; `+
							`fi`, agentLaunchAgentLabel),
					},
				},
			},
		}
		if _, err := ctlSendRequest(sock, bounceUserAgent, 10*time.Second, "agent-exec"); err != nil && verbose {
			fmt.Printf("warning: bounce user agent: %v\n", err)
		}

		// Restart the agent daemon via launchctl kickstart.
		fmt.Println("Restarting agent daemon (port 1024)...")
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
	}

	// Wait for the service manager to restart the agent.
	fmt.Println("Waiting for new agent...")
	time.Sleep(agentUpgradeReconnectInitialDelay)

	// Reconnect: the old vsock connection is dead, force a new one.
	connectReq := &controlpb.ControlRequest{Type: "agent-connect"}
	var connected bool
	for attempt := 0; attempt < agentUpgradeReconnectAttempts; attempt++ {
		resp, err = ctlSendRequest(sock, connectReq, agentUpgradeReconnectTimeout, "agent-connect")
		if err == nil && resp.Error == "" {
			connected = true
			break
		}
		fmt.Printf("  reconnect attempt %d...\n", attempt+1)
		time.Sleep(agentUpgradeReconnectDelay)
	}
	if !connected {
		return fmt.Errorf("%s", agentUpgradeReconnectTimeoutMessage())
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
	if err := agentstate.MarkVerifiedForSocket(sock, agentstate.SourceUpgrade); err != nil && verbose {
		fmt.Printf("warning: record guest agent capability: %v\n", err)
	}
	fmt.Printf("Upgraded: %s → %s\n", oldVersion, newVersion)
	return nil
}

func agentUpgradeReconnectTimeoutMessage() string {
	window := agentUpgradeReconnectInitialDelay + agentUpgradeReconnectAttempts*agentUpgradeReconnectDelay
	return fmt.Sprintf("agent installed and restart requested, but agent did not reconnect within %ds (tried %d reconnects); VM may still be restarting the agent, retry cove ctl agent-ping or cove agent-upgrade", int(window/time.Second), agentUpgradeReconnectAttempts)
}

func guestAgentUpgradeInstallScript(tmpPath, destPath string) string {
	return strings.Join([]string{
		"set -eu",
		"tmp=" + shellQuote(tmpPath),
		"dest=" + shellQuote(destPath),
		"chmod 755 \"$tmp\"",
		"chown root:root \"$tmp\" 2>/dev/null || chown root:wheel \"$tmp\" 2>/dev/null || true",
		"mv -f \"$tmp\" \"$dest\"",
		"chmod 755 \"$dest\"",
	}, "\n")
}

func linuxAgentRestartCommand() []string {
	return []string{"sh", "-lc", `cat >/tmp/cove-restart-vz-agent.sh <<'EOF'
#!/bin/sh
set -u
sleep 1
if command -v systemctl >/dev/null 2>&1 && systemctl list-unit-files vz-agent.service >/dev/null 2>&1; then
	exec systemctl restart vz-agent
fi
if command -v rc-service >/dev/null 2>&1 && test -x /etc/init.d/vz-agent; then
	rc-service vz-agent stop || pkill -KILL -x vz-agent || true
	rc-service vz-agent zap >/dev/null 2>&1 || true
	exec rc-service vz-agent start
fi
pkill -KILL -x vz-agent || true
exec /usr/local/bin/vz-agent -mode daemon >/var/log/vz-agent.log 2>&1
EOF
chmod 755 /tmp/cove-restart-vz-agent.sh
nohup /tmp/cove-restart-vz-agent.sh >/tmp/cove-restart-vz-agent.log 2>&1 &
`}
}

func isLinuxGuestOS(osVersion string) bool {
	osVersion = strings.ToLower(osVersion)
	return strings.Contains(osVersion, "linux") ||
		strings.Contains(osVersion, "alpine") ||
		strings.Contains(osVersion, "ubuntu") ||
		strings.Contains(osVersion, "debian") ||
		strings.Contains(osVersion, "fedora")
}

func agentBuildTargetOS(guestOS string) string {
	if isLinuxGuestOS(guestOS) {
		return "linux"
	}
	return "darwin"
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
