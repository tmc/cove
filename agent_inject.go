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
	"strconv"
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
	return provisionAgentForVM(currentVMSelection())
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
		if err := agentstate.MarkVerifiedForSocket(sock, agentstate.SourceProvision); err != nil && verbose {
			fmt.Printf("warning: record guest agent capability: %v\n", err)
		}
		return nil
	}
	fmt.Printf("Agent version %s != host %s, upgrading...\n", guestVer, hostVer)
	return upgradeAgentAt(sock)
}

// versionRelation describes how host and guest agent versions order.
// Used by the auto-upgrade decision: only versionGuestOlder is a safe upgrade.
type versionRelation int

const (
	// versionUnknown means we cannot rule out a downgrade. No auto-upgrade.
	versionUnknown versionRelation = iota
	// versionEqual means versions match exactly. No-op.
	versionEqual
	// versionGuestOlder means the guest is older than the host (semver compare).
	// Safe to auto-upgrade.
	versionGuestOlder
	// versionGuestNewer means the guest is newer than the host. Do NOT downgrade;
	// log a warning instead.
	versionGuestNewer
	// versionDifferent means we have two non-empty, non-comparable version
	// strings (e.g., two dev commit shas, or one tag and one sha). Treat as a
	// signal to upgrade since the host is the source of truth, but callers may
	// want to gate it more conservatively for production builds.
	versionDifferent
)

// agentVersionCompare reports how host and guest versions order.
// "" / "dev" / "unknown" on either side returns versionUnknown.
// Equal strings return versionEqual.
// If both look like semver (vX.Y.Z[...]) and differ, returns Older or Newer.
// Otherwise returns versionDifferent.
func agentVersionCompare(host, guest string) versionRelation {
	if host == "" || host == "dev" || host == "unknown" {
		return versionUnknown
	}
	if guest == "" || guest == "dev" || guest == "unknown" {
		return versionUnknown
	}
	if host == guest {
		return versionEqual
	}
	hostParts, hostOK := parseSemver(host)
	guestParts, guestOK := parseSemver(guest)
	if hostOK && guestOK {
		switch {
		case semverLess(guestParts, hostParts):
			return versionGuestOlder
		case semverLess(hostParts, guestParts):
			return versionGuestNewer
		default:
			return versionEqual
		}
	}
	return versionDifferent
}

// agentVersionsEqual reports whether host and guest agent versions should be
// treated as equivalent for the idempotent fast path. Empty/dev/unknown
// values mean we cannot prove equivalence and must rebuild.
func agentVersionsEqual(host, guest string) bool {
	return agentVersionCompare(host, guest) == versionEqual
}

// parseSemver extracts the major.minor.patch components of a vX.Y.Z[-pre]
// string. The leading "v" is optional. Pre-release suffixes are ignored for
// ordering — they only matter when X.Y.Z is identical and we already return
// versionEqual on identical strings.
func parseSemver(s string) ([3]int, bool) {
	var out [3]int
	if strings.HasPrefix(s, "v") {
		s = s[1:]
	}
	// Strip pre-release / build metadata: keep up to first '-' or '+'.
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// semverLess reports whether a < b in major.minor.patch order.
func semverLess(a, b [3]int) bool {
	for i := 0; i < 3; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// injectAgentOnly mounts the VM disk and injects the vz-agent binary,
// LaunchDaemon plist (port 1024), and LaunchAgent plist (port 1025).
// No user provisioning is performed.
func injectAgentOnly() error {
	return injectAgentOnlyForVM(currentVMSelection())
}

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
	if err := agentstate.MarkVerifiedForSocket(sock, agentstate.SourceUpgrade); err != nil && verbose {
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
