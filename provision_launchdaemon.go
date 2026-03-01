package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// shellEscape wraps a value in single quotes with proper escaping for safe
// embedding in shell scripts. Single quotes within the value are replaced
// with the sequence '\'' (end quote, escaped quote, start quote).
func shellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// injectLaunchDaemonProvisioning creates the LaunchDaemon and script for first-boot provisioning.
// Files needing root:wheel ownership are collected in rootFiles for a later targeted sudo chown.
func injectLaunchDaemonProvisioning(mountPoint string, config ProvisionConfig, rootFiles *[]string) error {
	// Write the self-contained provisioning script
	// On the Data volume, /var/db is at private/var/db
	scriptDir := filepath.Join(mountPoint, "private", "var", "db")
	if err := os.MkdirAll(scriptDir, 0755); err != nil {
		return fmt.Errorf("create script directory: %w", err)
	}

	scriptPath := filepath.Join(scriptDir, "vz-provision.sh")
	scriptContent := generateEmbeddedProvisionScript(config)
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0755); err != nil {
		return fmt.Errorf("write provision script: %w", err)
	}
	chownRootWheel(scriptPath, rootFiles)
	fmt.Printf("Written: %s\n", scriptPath)

	// Write the LaunchDaemon plist
	// On the Data volume, /Library/LaunchDaemons is at Library/LaunchDaemons
	launchDaemonsDir := filepath.Join(mountPoint, "Library", "LaunchDaemons")
	if err := os.MkdirAll(launchDaemonsDir, 0755); err != nil {
		return fmt.Errorf("create LaunchDaemons directory: %w", err)
	}

	plistPath := filepath.Join(launchDaemonsDir, "com.github.tmc.vz-macos.provision.plist")
	plistContent := generateEmbeddedLaunchDaemonPlist()
	if err := os.WriteFile(plistPath, []byte(plistContent), 0644); err != nil {
		return fmt.Errorf("write LaunchDaemon plist: %w", err)
	}
	chownRootWheel(plistPath, rootFiles)
	fmt.Printf("Written: %s\n", plistPath)

	return nil
}

// stageLaunchDaemonProvisioning stages the LaunchDaemon plist and provision
// script to the staging directory.
func stageLaunchDaemonProvisioning(stagingDir string, config ProvisionConfig, manifest *ProvisionManifest) error {
	scriptContent := generateEmbeddedProvisionScript(config)
	if err := stageFile(stagingDir, filepath.Join("private", "var", "db", "vz-provision.sh"),
		[]byte(scriptContent), 0755, "root:wheel", manifest); err != nil {
		return err
	}

	plistContent := generateEmbeddedLaunchDaemonPlist()
	if err := stageFile(stagingDir, filepath.Join("Library", "LaunchDaemons", "com.github.tmc.vz-macos.provision.plist"),
		[]byte(plistContent), 0644, "root:wheel", manifest); err != nil {
		return err
	}
	return nil
}

// generateEmbeddedLaunchDaemonPlist returns a LaunchDaemon plist that references
// the self-contained script at /var/db/vz-provision.sh (no VirtioFS dependency)
func generateEmbeddedLaunchDaemonPlist() string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.github.tmc.vz-macos.provision</string>
    <key>ProgramArguments</key>
    <array>
        <string>/bin/bash</string>
        <string>/var/db/vz-provision.sh</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>LaunchOnlyOnce</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/var/log/vz-provision.log</string>
    <key>StandardErrorPath</key>
    <string>/var/log/vz-provision.log</string>
    <key>WaitForDebugger</key>
    <false/>
</dict>
</plist>
`
}

// generateEmbeddedProvisionScript returns a self-contained provisioning script
// with all configuration embedded (no external file dependencies)
func generateEmbeddedProvisionScript(config ProvisionConfig) string {
	adminStr := "false"
	if config.Admin {
		adminStr = "true"
	}
	bootstrapStr := "false"
	if config.BootstrapRecovery {
		bootstrapStr = "true"
	}
	fullname := config.Fullname
	if fullname == "" {
		fullname = config.Username
	}
	xcodeStr := "false"
	if config.InstallXcodeCLI {
		xcodeStr = "true"
	}
	sshdStr := "false"
	if config.EnableSSHD {
		sshdStr = "true"
	}

	return fmt.Sprintf(`#!/bin/bash
# vz-macos self-contained provisioning script
# Generated with embedded configuration - no external dependencies
set -e

# Embedded configuration
USERNAME=%s
PASSWORD=%s
FULLNAME=%s
ADMIN="%s"
BOOTSTRAP_RECOVERY="%s"
INSTALL_XCODE_CLI="%s"
ENABLE_SSHD="%s"

MARKER="/var/db/.vz-provisioned"
LOG="/var/log/vz-provision.log"

log() {
    echo "$(date '+%%Y-%%m-%%d %%H:%%M:%%S') $1" >> "$LOG"
    echo "$1"
}

# Check if already provisioned
if [ -f "$MARKER" ]; then
    log "Already provisioned, exiting"
    exit 0
fi

log "Starting vz-macos provisioning..."
log "Username: $USERNAME"
log "Admin: $ADMIN"
log "Bootstrap recovery: $BOOTSTRAP_RECOVERY"

# Get next available UID (starting from 501 for regular users)
MAXID=$(dscl . -list /Users UniqueID 2>/dev/null | awk '{print $2}' | sort -n | tail -1)
if [ -z "$MAXID" ] || [ "$MAXID" -lt 500 ]; then
    MAXID=500
fi
NEWID=$((MAXID + 1))

log "Assigning UID: $NEWID"

if [ "$BOOTSTRAP_RECOVERY" = "true" ]; then
    # Two-user bootstrap: create a hidden bootstrap admin first, then create the
    # real user using the bootstrap admin's credentials. The real user, created BY
    # a SecureToken-bearing admin, gets full recovery authorization.
    BOOTSTRAP_USER="_vzbootstrap"
    BOOTSTRAP_PASS="$(head -c 32 /dev/urandom | base64 | tr -d '/+=' | head -c 24)"
    BOOTSTRAP_UID=$((NEWID))
    NEWID=$((NEWID + 1))

    log "Creating bootstrap admin ($BOOTSTRAP_USER, UID=$BOOTSTRAP_UID)..."
    sysadminctl -addUser "$BOOTSTRAP_USER" \
        -password "$BOOTSTRAP_PASS" \
        -fullName "VZ Bootstrap" \
        -UID "$BOOTSTRAP_UID" \
        -shell /usr/bin/false \
        -home "/var/empty" \
        -admin 2>&1 | tee -a "$LOG"

    # Hide the bootstrap user from login window
    dscl . -create /Users/"$BOOTSTRAP_USER" IsHidden 1 2>&1 | tee -a "$LOG" || true

    log "Creating user $USERNAME via bootstrap admin (UID=$NEWID)..."
    if [ "$ADMIN" = "true" ]; then
        sysadminctl -addUser "$USERNAME" \
            -password "$PASSWORD" \
            -fullName "$FULLNAME" \
            -UID "$NEWID" \
            -shell /bin/zsh \
            -home "/Users/$USERNAME" \
            -admin \
            -adminUser "$BOOTSTRAP_USER" \
            -adminPassword "$BOOTSTRAP_PASS" 2>&1 | tee -a "$LOG"
    else
        sysadminctl -addUser "$USERNAME" \
            -password "$PASSWORD" \
            -fullName "$FULLNAME" \
            -UID "$NEWID" \
            -shell /bin/zsh \
            -home "/Users/$USERNAME" \
            -adminUser "$BOOTSTRAP_USER" \
            -adminPassword "$BOOTSTRAP_PASS" 2>&1 | tee -a "$LOG"
    fi
else
    # Standard single-user creation
    if command -v sysadminctl &> /dev/null; then
        log "Using sysadminctl to create user..."
        if [ "$ADMIN" = "true" ]; then
            sysadminctl -addUser "$USERNAME" \
                -password "$PASSWORD" \
                -fullName "$FULLNAME" \
                -UID "$NEWID" \
                -shell /bin/zsh \
                -home "/Users/$USERNAME" \
                -admin 2>&1 | tee -a "$LOG"
        else
            sysadminctl -addUser "$USERNAME" \
                -password "$PASSWORD" \
                -fullName "$FULLNAME" \
                -UID "$NEWID" \
                -shell /bin/zsh \
                -home "/Users/$USERNAME" 2>&1 | tee -a "$LOG"
        fi
    else
        # Fallback to dscl
        log "Using dscl to create user..."
        dscl . -create /Users/"$USERNAME"
        dscl . -create /Users/"$USERNAME" UserShell /bin/zsh
        dscl . -create /Users/"$USERNAME" RealName "$FULLNAME"
        dscl . -create /Users/"$USERNAME" UniqueID "$NEWID"
        dscl . -create /Users/"$USERNAME" PrimaryGroupID 20
        dscl . -create /Users/"$USERNAME" NFSHomeDirectory /Users/"$USERNAME"
        dscl . -passwd /Users/"$USERNAME" "$PASSWORD"

        # Add to admin group if requested
        if [ "$ADMIN" = "true" ]; then
            log "Adding $USERNAME to admin group..."
            dseditgroup -o edit -a "$USERNAME" -t user admin 2>&1 | tee -a "$LOG" || true
        fi
    fi
fi

# Create home directory
log "Creating home directory..."
createhomedir -c -u "$USERNAME" 2>&1 | tee -a "$LOG" || true

# Verify user was created
if dscl . -read /Users/"$USERNAME" &>/dev/null; then
    log "SUCCESS: User $USERNAME created successfully"
else
    log "ERROR: Failed to create user $USERNAME"
    exit 1
fi

if [ "$INSTALL_XCODE_CLI" = "true" ]; then
    log "Installing Xcode Command Line Tools..."
    # Create the placeholder file that triggers CLI tools to appear in softwareupdate
    touch /tmp/.com.apple.dt.CommandLineTools.installondemand.in-progress
    # Find the specific product name for the Command Line Tools
    PROD=$(softwareupdate -l | grep "\*.*Command Line Tools" | head -n 1 | awk -F"*" '{print $2}' | sed -e 's/^ *//' | sed 's/Label: //g' | tr -d '\n')
    if [ -n "$PROD" ]; then
        log "Found CLI tools: $PROD"
        softwareupdate -i "$PROD" --verbose 2>&1 | tee -a "$LOG" || true
    else
        log "Warning: Command Line Tools not found in softwareupdate"
    fi
fi

# Update preboot volume to register user for recovery authorization.
# Without this, the user may have a SecureToken but not be recognized
# by recovery mode for LocalPolicy operations (csrutil, etc).
log "Updating preboot volume for recovery authorization..."
diskutil apfs updatePreboot / 2>&1 | tee -a "$LOG" || true

# Check SecureToken status
log "Checking SecureToken status..."
sysadminctl -secureTokenStatus "$USERNAME" 2>&1 | tee -a "$LOG" || true

# Enable SSH daemon (Remote Login) if requested
if [ "$ENABLE_SSHD" = "true" ]; then
    log "Enabling SSH daemon (Remote Login)..."
    systemsetup -setremotelogin on 2>&1 | tee -a "$LOG" || true
    # Fix SSH key ownership if present
    if [ -d "/Users/$USERNAME/.ssh" ]; then
        chown -R "$NEWID:20" "/Users/$USERNAME/.ssh"
        chmod 700 "/Users/$USERNAME/.ssh"
        [ -f "/Users/$USERNAME/.ssh/authorized_keys" ] && chmod 600 "/Users/$USERNAME/.ssh/authorized_keys"
    fi
    log "SSH daemon enabled"
fi

# Mark as provisioned
touch "$MARKER"
log "Provisioning marker created: $MARKER"

# Self-cleanup: remove the LaunchDaemon and script so they don't run again
rm -f /Library/LaunchDaemons/com.github.tmc.vz-macos.provision.plist 2>/dev/null || true
rm -f /var/db/vz-provision.sh 2>/dev/null || true
log "LaunchDaemon and script removed (self-cleanup)"

# Configure automatic login for the created user
log "Configuring automatic login..."
defaults write /Library/Preferences/com.apple.loginwindow autoLoginUser "$USERNAME" 2>/dev/null || true
log "Auto-login configured for: $USERNAME"

log "=== Provisioning complete ==="

# Restart loginwindow so it picks up the newly created user and auto-login
# settings. Without this, loginwindow starts before the user exists and
# shows the login screen on first boot.
log "Restarting loginwindow for auto-login..."
killall loginwindow 2>/dev/null || true

exit 0
`, shellEscape(config.Username), shellEscape(config.Password), shellEscape(fullname), adminStr, bootstrapStr, xcodeStr, sshdStr)
}

// LaunchDaemon plist that runs provisioning script on first boot
// DEPRECATED: Use generateEmbeddedLaunchDaemonPlist() for self-contained provisioning
const launchDaemonPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.github.tmc.vz-macos.provision</string>
    <key>ProgramArguments</key>
    <array>
        <string>/bin/bash</string>
        <string>/Volumes/My Shared Files/scripts/provision.sh</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>LaunchOnlyOnce</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/var/log/vz-provision.log</string>
    <key>StandardErrorPath</key>
    <string>/var/log/vz-provision.log</string>
    <key>WaitForDebugger</key>
    <false/>
</dict>
</plist>
`

// Provisioning script that creates a user account
const provisionScript = `#!/bin/bash
# vz-macos provisioning script
# This script runs once on first boot to create a user account

set -e

PROVISION_DIR="/Volumes/My Shared Files"
CONFIG="$PROVISION_DIR/config/user.json"
MARKER="/var/db/.vz-provisioned"
LOG="/var/log/vz-provision.log"

log() {
    echo "$(date '+%Y-%m-%d %H:%M:%S') $1" >> "$LOG"
    echo "$1"
}

# Check if already provisioned
if [ -f "$MARKER" ]; then
    log "Already provisioned, exiting"
    exit 0
fi

log "Starting vz-macos provisioning..."

# Wait for shared directory to be mounted (up to 60 seconds)
WAIT=0
while [ ! -d "$PROVISION_DIR" ] && [ $WAIT -lt 60 ]; do
    log "Waiting for shared directory..."
    sleep 2
    WAIT=$((WAIT + 2))
done

if [ ! -f "$CONFIG" ]; then
    log "ERROR: Config file not found: $CONFIG"
    log "Make sure to run VM with: vz-macos run -share-dir ~/.vz/provision"
    exit 1
fi

# Parse JSON config (using basic grep/sed since jq may not be available)
USERNAME=$(grep -o '"username"[[:space:]]*:[[:space:]]*"[^"]*"' "$CONFIG" | sed 's/.*:.*"\([^"]*\)"/\1/')
PASSWORD=$(grep -o '"password"[[:space:]]*:[[:space:]]*"[^"]*"' "$CONFIG" | sed 's/.*:.*"\([^"]*\)"/\1/')
FULLNAME=$(grep -o '"fullname"[[:space:]]*:[[:space:]]*"[^"]*"' "$CONFIG" | sed 's/.*:.*"\([^"]*\)"/\1/')
ADMIN=$(grep -o '"admin"[[:space:]]*:[[:space:]]*[a-z]*' "$CONFIG" | grep -o 'true\|false')

if [ -z "$USERNAME" ]; then
    log "ERROR: No username in config"
    exit 1
fi

if [ -z "$FULLNAME" ]; then
    FULLNAME="$USERNAME"
fi

log "Creating user: $USERNAME ($FULLNAME)"

# Get next available UID (starting from 501 for regular users)
MAXID=$(dscl . -list /Users UniqueID 2>/dev/null | awk '{print $2}' | sort -n | tail -1)
if [ -z "$MAXID" ] || [ "$MAXID" -lt 500 ]; then
    MAXID=500
fi
NEWID=$((MAXID + 1))

log "Assigning UID: $NEWID"

# Create user with sysadminctl (preferred method for modern macOS)
if command -v sysadminctl &> /dev/null; then
    log "Using sysadminctl to create user..."
    sysadminctl -addUser "$USERNAME" \
        -password "$PASSWORD" \
        -fullName "$FULLNAME" \
        -UID "$NEWID" \
        -shell /bin/zsh \
        -home "/Users/$USERNAME" \
        -admin 2>&1 | tee -a "$LOG"
else
    # Fallback to dscl
    log "Using dscl to create user..."
    dscl . -create /Users/"$USERNAME"
    dscl . -create /Users/"$USERNAME" UserShell /bin/zsh
    dscl . -create /Users/"$USERNAME" RealName "$FULLNAME"
    dscl . -create /Users/"$USERNAME" UniqueID "$NEWID"
    dscl . -create /Users/"$USERNAME" PrimaryGroupID 20
    dscl . -create /Users/"$USERNAME" NFSHomeDirectory /Users/"$USERNAME"
    dscl . -passwd /Users/"$USERNAME" "$PASSWORD"
fi

# Add to admin group if requested
if [ "$ADMIN" = "true" ]; then
    log "Adding $USERNAME to admin group..."
    dseditgroup -o edit -a "$USERNAME" -t user admin 2>&1 | tee -a "$LOG" || true
fi

# Create home directory
log "Creating home directory..."
createhomedir -c -u "$USERNAME" 2>&1 | tee -a "$LOG" || true

# Verify user was created
if dscl . -read /Users/"$USERNAME" &>/dev/null; then
    log "SUCCESS: User $USERNAME created successfully"
else
    log "ERROR: Failed to create user $USERNAME"
    exit 1
fi

# Update preboot volume for recovery authorization
log "Updating preboot volume for recovery authorization..."
diskutil apfs updatePreboot / 2>&1 | tee -a "$LOG" || true

# Mark as provisioned
touch "$MARKER"
log "Provisioning marker created: $MARKER"

# Self-cleanup: remove the LaunchDaemon so it doesn't run again
rm -f /Library/LaunchDaemons/com.github.tmc.vz-macos.provision.plist 2>/dev/null || true
log "LaunchDaemon removed (self-cleanup)"

log "=== Provisioning complete ==="
log "You can now log in as: $USERNAME"

# Optional: Skip Setup Assistant (create .AppleSetupDone marker)
# This requires the user to already exist, which we've done above
touch /var/db/.AppleSetupDone 2>/dev/null || true
log "Created .AppleSetupDone marker"

# Configure automatic login for the created user
log "Configuring automatic login..."
# Method 1: Use kcpassword (works on all macOS versions)
# This requires the password to be encoded in a specific format
# For simplicity, we use defaults write which works in some versions

# Method 2: Use defaults write (may require restart)
sudo defaults write /Library/Preferences/com.apple.loginwindow autoLoginUser "$USERNAME" 2>/dev/null || true

# Method 3: Create kcpassword file (encoded password)
# The kcpassword file uses a simple XOR cipher with the key:
# 7D 89 52 23 D2 BC DD EA A3 B9 1F
# For robustness, we attempt multiple methods

if [ -f /etc/kcpassword ]; then
    log "Auto-login appears to be configured via kcpassword"
else
    # Try to configure auto-login via security framework
    log "Note: For full auto-login, you may need to configure in System Preferences"
fi

log "Auto-login configured for: $USERNAME"

exit 0
`
