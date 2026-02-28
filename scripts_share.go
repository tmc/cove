// scripts_share.go - VirtioFS share for guest scripts and agent bootstrap
//
// This provides infrastructure for sharing a host-side scripts directory
// with the guest VM via VirtioFS. The scripts directory can contain:
//
//   - Guest agent bootstrap script (run at boot)
//   - Custom provisioning scripts
//   - Configuration files
//   - Future: vsock-based guest agent binary
//
// The share appears at /Volumes/vz-scripts in the guest (macOS) or can be
// mounted manually in Linux guests via: mount -t virtiofs vz-scripts /mnt
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tmc/apple/foundation"
	vz "github.com/tmc/apple/virtualization"
)

// ScriptsShareTag is the VirtioFS tag for the scripts share
const ScriptsShareTag = "vz-scripts"

// ScriptsShareConfig holds configuration for the scripts VirtioFS share
type ScriptsShareConfig struct {
	Enabled   bool   // Whether to enable scripts share
	HostPath  string // Path on host (default: vmDir/scripts/)
	ReadOnly  bool   // Mount as read-only in guest
	RunOnBoot bool   // Run bootstrap script on boot via LaunchDaemon
}

// DefaultScriptsPath returns the default scripts directory path for a VM
func DefaultScriptsPath(vmDir string) string {
	return filepath.Join(vmDir, "scripts")
}

// EnsureScriptsDir creates the scripts directory and populates with default files
func EnsureScriptsDir(path string) error {
	// Create scripts directory
	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("create scripts directory: %w", err)
	}

	// Create subdirectories
	subdirs := []string{
		"boot.d",    // Scripts run at boot (in order)
		"on-demand", // Scripts run on-demand via control socket
		"agent",     // Guest agent files
	}
	for _, subdir := range subdirs {
		if err := os.MkdirAll(filepath.Join(path, subdir), 0755); err != nil {
			return fmt.Errorf("create %s directory: %w", subdir, err)
		}
	}

	// Write bootstrap script if it doesn't exist
	bootstrapPath := filepath.Join(path, "bootstrap.sh")
	if _, err := os.Stat(bootstrapPath); os.IsNotExist(err) {
		if err := os.WriteFile(bootstrapPath, []byte(guestAgentBootstrapScript), 0755); err != nil {
			return fmt.Errorf("write bootstrap script: %w", err)
		}
		fmt.Printf("Created bootstrap script: %s\n", bootstrapPath)
	}

	// Write example boot.d script
	exampleBootPath := filepath.Join(path, "boot.d", "00-example.sh.disabled")
	if _, err := os.Stat(exampleBootPath); os.IsNotExist(err) {
		if err := os.WriteFile(exampleBootPath, []byte(exampleBootScript), 0644); err != nil {
			return fmt.Errorf("write example boot script: %w", err)
		}
	}

	// Write agent stub
	agentStubPath := filepath.Join(path, "agent", "README.md")
	if _, err := os.Stat(agentStubPath); os.IsNotExist(err) {
		if err := os.WriteFile(agentStubPath, []byte(agentReadme), 0644); err != nil {
			return fmt.Errorf("write agent readme: %w", err)
		}
	}

	return nil
}

// CreateScriptsShareConfig creates a VirtioFS configuration for the scripts share
func CreateScriptsShareConfig(config ScriptsShareConfig) (vz.VZVirtioFileSystemDeviceConfiguration, error) {
	if !config.Enabled {
		return vz.VZVirtioFileSystemDeviceConfiguration{}, nil
	}

	// Ensure scripts directory exists
	if err := EnsureScriptsDir(config.HostPath); err != nil {
		return vz.VZVirtioFileSystemDeviceConfiguration{}, err
	}

	// Create file system device configuration with our tag
	fsConfig := vz.NewVirtioFileSystemDeviceConfigurationWithTag(ScriptsShareTag)
	if fsConfig.ID == 0 {
		return vz.VZVirtioFileSystemDeviceConfiguration{}, fmt.Errorf("failed to create file system device configuration")
	}
	fsConfig.Retain()

	// Create shared directory pointing to host path
	sharedURL := foundation.NewURLFileURLWithPath(config.HostPath)
	sharedURL.Retain()

	// Create VZSharedDirectory with read-only setting
	sharedDir := vz.NewSharedDirectoryWithURLReadOnly(sharedURL, config.ReadOnly)
	if sharedDir.ID == 0 {
		return vz.VZVirtioFileSystemDeviceConfiguration{}, fmt.Errorf("failed to create shared directory")
	}
	sharedDir.Retain()

	// Create single directory share
	singleShare := vz.NewSingleDirectoryShareWithDirectory(&sharedDir)
	if singleShare.ID == 0 {
		return vz.VZVirtioFileSystemDeviceConfiguration{}, fmt.Errorf("failed to create single directory share")
	}
	singleShare.Retain()

	// Set the share on the file system config
	fsConfig.SetShare(&singleShare.VZDirectoryShare)

	mode := "rw"
	if config.ReadOnly {
		mode = "ro"
	}
	fmt.Printf("  Scripts share: %s -> /Volumes/%s (%s)\n", config.HostPath, ScriptsShareTag, mode)

	return fsConfig, nil
}

// InjectScriptsRunnerLaunchDaemon injects a LaunchDaemon that runs scripts from the VirtioFS share
func InjectScriptsRunnerLaunchDaemon(mountPoint string, config ScriptsShareConfig, rootFiles *[]string) error {
	if !config.RunOnBoot {
		return nil
	}

	fmt.Println("Injecting scripts runner LaunchDaemon...")

	// Create the runner script
	scriptDir := filepath.Join(mountPoint, "private", "var", "db")
	if err := os.MkdirAll(scriptDir, 0755); err != nil {
		return fmt.Errorf("create script directory: %w", err)
	}

	scriptPath := filepath.Join(scriptDir, "vz-scripts-runner.sh")
	if err := os.WriteFile(scriptPath, []byte(scriptsRunnerScript), 0755); err != nil {
		return fmt.Errorf("write runner script: %w", err)
	}
	chownRootWheel(scriptPath, rootFiles)
	fmt.Printf("Written: %s\n", scriptPath)

	// Create the LaunchDaemon plist
	launchDaemonsDir := filepath.Join(mountPoint, "Library", "LaunchDaemons")
	if err := os.MkdirAll(launchDaemonsDir, 0755); err != nil {
		return fmt.Errorf("create LaunchDaemons directory: %w", err)
	}

	plistPath := filepath.Join(launchDaemonsDir, "com.vz.scripts-runner.plist")
	if err := os.WriteFile(plistPath, []byte(scriptsRunnerPlist), 0644); err != nil {
		return fmt.Errorf("write LaunchDaemon plist: %w", err)
	}
	chownRootWheel(plistPath, rootFiles)
	fmt.Printf("Written: %s\n", plistPath)

	return nil
}

// scriptsRunnerPlist is the LaunchDaemon that runs the scripts runner at boot
const scriptsRunnerPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.vz.scripts-runner</string>
    <key>ProgramArguments</key>
    <array>
        <string>/bin/bash</string>
        <string>/var/db/vz-scripts-runner.sh</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/var/log/vz-scripts.log</string>
    <key>StandardErrorPath</key>
    <string>/var/log/vz-scripts.log</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>VZ_SCRIPTS_DIR</key>
        <string>/Volumes/vz-scripts</string>
    </dict>
</dict>
</plist>
`

// scriptsRunnerScript runs scripts from the VirtioFS share on boot
const scriptsRunnerScript = `#!/bin/bash
# vz-macos Scripts Runner
# Runs scripts from the VirtioFS share at boot
set -e

LOG="/var/log/vz-scripts.log"
SCRIPTS_DIR="${VZ_SCRIPTS_DIR:-/Volumes/vz-scripts}"
MARKER="/var/db/.vz-scripts-bootstrap-done"

log() {
    echo "$(date '+%Y-%m-%d %H:%M:%S') [scripts-runner] $1" >> "$LOG"
    echo "$1"
}

log "=== vz-macos Scripts Runner ==="
log "Scripts directory: $SCRIPTS_DIR"

# Wait for VirtioFS mount (up to 60 seconds)
WAIT=0
while [ ! -d "$SCRIPTS_DIR" ] && [ $WAIT -lt 60 ]; do
    log "Waiting for scripts share to mount..."
    sleep 2
    WAIT=$((WAIT + 2))
done

if [ ! -d "$SCRIPTS_DIR" ]; then
    log "ERROR: Scripts directory not found after 60 seconds"
    log "Make sure VM was started with -scripts flag"
    exit 1
fi

log "Scripts share mounted at: $SCRIPTS_DIR"

# Run bootstrap script (once)
BOOTSTRAP="$SCRIPTS_DIR/bootstrap.sh"
if [ -f "$BOOTSTRAP" ] && [ ! -f "$MARKER" ]; then
    log "Running bootstrap script: $BOOTSTRAP"
    if bash "$BOOTSTRAP" 2>&1 | tee -a "$LOG"; then
        touch "$MARKER"
        log "Bootstrap complete"
    else
        log "ERROR: Bootstrap script failed"
    fi
elif [ -f "$MARKER" ]; then
    log "Bootstrap already completed (marker exists)"
fi

# Run boot.d scripts (every boot, in order)
BOOTD="$SCRIPTS_DIR/boot.d"
if [ -d "$BOOTD" ]; then
    log "Running boot.d scripts..."
    for script in "$BOOTD"/*.sh; do
        if [ -f "$script" ] && [ -x "$script" ]; then
            name=$(basename "$script")
            log "Running: $name"
            if bash "$script" 2>&1 | tee -a "$LOG"; then
                log "  $name: OK"
            else
                log "  $name: FAILED (exit code $?)"
            fi
        fi
    done
fi

log "=== Scripts Runner Complete ==="
exit 0
`

// guestAgentBootstrapScript is the initial guest agent bootstrap
// This sets up the environment for the vsock-based guest agent
const guestAgentBootstrapScript = `#!/bin/bash
# vz-macos Guest Agent Bootstrap
# This script runs once on first boot to prepare the guest agent environment
set -e

LOG="/var/log/vz-agent-bootstrap.log"
SCRIPTS_DIR="${VZ_SCRIPTS_DIR:-/Volumes/vz-scripts}"

log() {
    echo "$(date '+%Y-%m-%d %H:%M:%S') [agent-bootstrap] $1" >> "$LOG"
    echo "$1"
}

log "=== Guest Agent Bootstrap ==="
log "Host: $(hostname)"
log "macOS Version: $(sw_vers -productVersion)"
log "Scripts Directory: $SCRIPTS_DIR"

# Create agent working directory
AGENT_DIR="/var/db/vz-agent"
mkdir -p "$AGENT_DIR"
log "Created agent directory: $AGENT_DIR"

# Create socket directory for future vsock agent
SOCK_DIR="/var/run/vz-agent"
mkdir -p "$SOCK_DIR"
chmod 755 "$SOCK_DIR"
log "Created socket directory: $SOCK_DIR"

# Write agent configuration stub
cat > "$AGENT_DIR/config.json" << 'EOF'
{
    "version": "0.1.0",
    "vsock_port": 1024,
    "scripts_dir": "/Volumes/vz-scripts",
    "log_file": "/var/log/vz-agent.log",
    "features": {
        "exec": true,
        "file_transfer": true,
        "port_forward": true,
        "clipboard": false
    }
}
EOF
log "Wrote agent config: $AGENT_DIR/config.json"

# Enable SSH for remote access (if not already enabled)
if ! systemsetup -getremotelogin 2>/dev/null | grep -q "On"; then
    log "Enabling Remote Login (SSH)..."
    systemsetup -setremotelogin on 2>/dev/null || log "Warning: Could not enable SSH"
fi

# Report network info
log "Network interfaces:"
ifconfig -a | grep -E "^[a-z]|inet " | while read line; do
    log "  $line"
done

log "=== Bootstrap Complete ==="
log "Guest agent infrastructure is ready."
log "Future: vsock agent will listen on port 1024"
exit 0
`

// exampleBootScript is an example boot.d script (disabled by default)
const exampleBootScript = `#!/bin/bash
# Example boot.d script
# Rename to remove .disabled suffix to enable
# Scripts in boot.d/ run on every boot, in alphabetical order

echo "Example boot.d script running at $(date)"

# Add your custom boot-time commands here
# Examples:
#   - Mount additional volumes
#   - Start custom services
#   - Configure network settings
#   - Run health checks
`

// agentReadme describes the guest agent architecture
const agentReadme = `# vz-macos Guest Agent

This directory contains files for the vsock-based guest agent.

## Architecture

The guest agent provides a control channel between host and guest:

` + "```" + `
Host                          Guest
┌────────────────┐            ┌────────────────┐
│ vz-macos       │            │ vz-agent       │
│                │            │                │
│ Control Socket │◄──vsock───►│ Agent Daemon   │
│ (Unix socket)  │   port     │ (launchd)      │
│                │   1024     │                │
└────────────────┘            └────────────────┘
` + "```" + `

## Features (Planned)

- **exec**: Run commands in guest
- **file_transfer**: Copy files host <-> guest
- **port_forward**: Forward ports through vsock
- **clipboard**: Shared clipboard (future)

## Files

- ` + "`config.json`" + ` - Agent configuration (created by bootstrap)
- ` + "`vz-agent`" + ` - Agent binary (future)

## Manual Agent Start

` + "```bash" + `
# The agent will be started automatically via LaunchDaemon
# For manual testing:
/Volumes/vz-scripts/agent/vz-agent -config /var/db/vz-agent/config.json
` + "```" + `

## Host-Side Control

` + "```bash" + `
# Execute command in guest
./vz-macos ctl exec "uname -a"

# Copy file to guest
./vz-macos ctl copy-to local.txt /tmp/remote.txt

# Copy file from guest
./vz-macos ctl copy-from /tmp/remote.txt local.txt

# Forward port
./vz-macos ctl port-forward 8080:80
` + "```" + `
`

// GetScriptsShareConfig builds ScriptsShareConfig from CLI flags
func GetScriptsShareConfig(enabled bool, hostPath string, readOnly bool, runOnBoot bool) ScriptsShareConfig {
	path := hostPath
	if path == "" && enabled {
		path = DefaultScriptsPath(vmDir)
	}

	return ScriptsShareConfig{
		Enabled:   enabled,
		HostPath:  path,
		ReadOnly:  readOnly,
		RunOnBoot: runOnBoot,
	}
}
