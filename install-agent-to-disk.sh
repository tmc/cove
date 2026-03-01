#!/bin/bash
# Install vz-agent into a mounted VM Data volume.
# Usage: sudo bash install-agent-to-disk.sh [mount_point] [data_partition]
set -e

MOUNT="${1:-/Volumes/Data}"
DATA_PART="${2:-disk26s5}"

if [ ! -d "$MOUNT" ]; then
    echo "Error: mount point $MOUNT does not exist"
    exit 1
fi

echo "Installing vz-agent to $MOUNT..."

# Enable APFS ownership
diskutil enableOwnership "$DATA_PART" >/dev/null 2>&1 || true

# Create directories
mkdir -p "$MOUNT/usr/local/bin"
mkdir -p "$MOUNT/Library/LaunchDaemons"

# Build agent if needed
AGENT_BIN="${AGENT_BIN:-$(dirname "$0")/vz-agent-guest}"
if [ ! -f "$AGENT_BIN" ]; then
    echo "Building vz-agent..."
    cd "$(dirname "$0")"
    CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o "$AGENT_BIN" github.com/tmc/vz-macos/cmd/vz-agent
fi

# Copy binary
cp "$AGENT_BIN" "$MOUNT/usr/local/bin/vz-agent"
chmod 755 "$MOUNT/usr/local/bin/vz-agent"
chown root:wheel "$MOUNT/usr/local/bin/vz-agent"
echo "  Installed: $MOUNT/usr/local/bin/vz-agent"

# Write LaunchDaemon plist
cat > "$MOUNT/Library/LaunchDaemons/com.vz.agent.plist" << 'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.vz.agent</string>
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
PLIST
chmod 644 "$MOUNT/Library/LaunchDaemons/com.vz.agent.plist"
chown root:wheel "$MOUNT/Library/LaunchDaemons/com.vz.agent.plist"
echo "  Installed: $MOUNT/Library/LaunchDaemons/com.vz.agent.plist"

echo "Done. Agent will start on next VM boot."
