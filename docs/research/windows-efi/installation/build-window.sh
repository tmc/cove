#!/bin/bash
set -euo pipefail
src=$(cd "$(dirname "$0")" && pwd)
out=${1:?usage: build-window.sh output-directory}
app="$out/Windows on VZ.app"
mkdir -p "$app/Contents/MacOS"
cat > "$app/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>windows-vz-window</string>
<key>CFBundleIdentifier</key><string>com.tmc.cove.windows-research-viewer</string>
<key>CFBundleName</key><string>Windows on VZ</string>
<key>CFBundleDisplayName</key><string>Windows on VZ</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleVersion</key><string>1</string>
<key>CFBundleShortVersionString</key><string>0.1</string>
<key>NSHighResolutionCapable</key><true/>
</dict></plist>
PLIST
xcrun swiftc "$src/window.swift" -o "$app/Contents/MacOS/windows-vz-window" -framework AppKit
codesign -s - -f "$app"
codesign --verify --strict "$app"
printf '%s\n' "$app"
