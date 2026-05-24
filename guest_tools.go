// guest_tools.go - Download and inject UTM's pre-built SPICE guest tools.
//
// UTM distributes macOS SPICE guest tools as a .pkg installer inside a .img
// disk image. The tools provide host↔guest clipboard sharing via the SPICE
// agent protocol (VZSpiceAgentPortAttachment on the host side).
//
// The injection workflow:
//
//  1. Download utm-guest-tools-macos-<version>.img from GitHub releases
//  2. Mount the .img to extract the .pkg installer
//  3. Copy the .pkg into the VM disk image at /var/db/vz-guest-tools.pkg
//  4. Install a LaunchDaemon that runs "installer -pkg" on first boot
//
// After installation, spice-vdagent and spice-vdagentd run as system services.
// macOS may prompt the user to allow these binaries on first launch.
//
// Requirements: macOS 13+ host, macOS 15+ guest for clipboard sharing.

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	// guestToolsVersion is the UTM SPICE guest tools version.
	guestToolsVersion = "0.22.1"

	// guestToolsIMGName is the disk image filename.
	guestToolsIMGName = "utm-guest-tools-macos-" + guestToolsVersion + ".img"

	// guestToolsPKGName is the package installer filename inside the .img.
	guestToolsPKGName = "spice-vdagent-" + guestToolsVersion + ".pkg"

	// guestToolsDownloadURL is the GitHub release download URL.
	guestToolsDownloadURL = "https://github.com/utmapp/vd_agent/releases/download/spice-vdagent-" + guestToolsVersion + "-macOS/" + guestToolsIMGName

	// guestToolsLaunchDaemonLabel is the launchd label for the installer.
	guestToolsLaunchDaemonLabel = "com.github.tmc.vz-macos.guest-tools-install"
)

// guestToolsCacheDir returns the directory for cached guest tools downloads.
// Falls back to the VM directory if the default cache is not writable
// (e.g., owned by root from a previous sudo run).
func guestToolsCacheDir() string {
	home, _ := os.UserHomeDir()
	primary := filepath.Join(home, ".vz", "guest-tools")

	// Check if the primary cache dir is writable. It may be owned by root
	// from a previous sudo inject run.
	if info, err := os.Stat(primary); err == nil {
		// Directory exists — try to write a temp file to check permissions.
		testPath := filepath.Join(primary, ".write-test")
		if f, err := os.Create(testPath); err == nil {
			f.Close()
			os.Remove(testPath)
			return primary
		}
		// Not writable. Try to fix ownership silently.
		if info.IsDir() {
			if uid := os.Getuid(); uid != 0 {
				// Suggest fix but fall back to vmDir.
				fmt.Printf("Note: %s is not writable (try: sudo chown -R %d %s)\n", primary, uid, primary)
			}
		}
		return filepath.Join(vmDir, "guest-tools")
	}

	return primary
}

// ensureGuestToolsPkg downloads the UTM guest tools .img, extracts the .pkg,
// and returns the path to the cached .pkg file. It checks multiple cache
// locations: the primary cache dir and the VM directory.
func ensureGuestToolsPkg() (string, error) {
	cacheDir := guestToolsCacheDir()
	pkgPath := filepath.Join(cacheDir, guestToolsPKGName)

	// Also check for a copy in the VM directory (survives interrupted inject).
	vmPkgPath := filepath.Join(vmDir, guestToolsPKGName)

	// Return cached .pkg if it exists and has reasonable size.
	if info, err := os.Stat(pkgPath); err == nil && info.Size() > 100000 {
		return pkgPath, nil
	}
	if info, err := os.Stat(vmPkgPath); err == nil && info.Size() > 100000 {
		fmt.Printf("  Using cached pkg from VM directory: %s\n", vmPkgPath)
		return vmPkgPath, nil
	}

	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", fmt.Errorf("create cache directory: %w", err)
	}

	imgPath := filepath.Join(cacheDir, guestToolsIMGName)

	// Download .img if not cached.
	if _, err := os.Stat(imgPath); os.IsNotExist(err) {
		fmt.Printf("Downloading UTM guest tools (%s)...\n", guestToolsVersion)
		if err := downloadGuestToolsIMG(imgPath); err != nil {
			return "", fmt.Errorf("download guest tools: %w", err)
		}
	}

	// Mount .img and extract .pkg.
	fmt.Println("Extracting guest tools package...")
	if err := extractPkgFromIMG(imgPath, pkgPath); err != nil {
		return "", fmt.Errorf("extract pkg: %w", err)
	}

	// Also stash a copy in the VM directory for resilience.
	if data, err := os.ReadFile(pkgPath); err == nil {
		if err := os.WriteFile(vmPkgPath, data, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "warning: cache guest tools pkg: %v\n", err)
		}
	}

	return pkgPath, nil
}

// downloadGuestToolsIMG downloads the .img disk image using curl.
func downloadGuestToolsIMG(destPath string) error {
	start := time.Now()

	cmd := exec.Command("curl", "-L", "-f", "-o", destPath, guestToolsDownloadURL)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Printf("  URL: %s\n", guestToolsDownloadURL)

	if err := cmd.Run(); err != nil {
		os.Remove(destPath)
		return fmt.Errorf("curl failed: %w", err)
	}

	info, err := os.Stat(destPath)
	if err != nil {
		return fmt.Errorf("stat downloaded file: %w", err)
	}

	fmt.Printf("  Downloaded %.1f MB in %v\n",
		float64(info.Size())/(1024*1024),
		time.Since(start).Truncate(time.Millisecond))

	return nil
}

// extractPkgFromIMG mounts the .img, copies the .pkg out, and detaches.
func extractPkgFromIMG(imgPath, pkgDest string) error {
	// Attach the disk image.
	cmd := exec.Command("hdiutil", "attach", imgPath, "-nobrowse", "-readonly", "-mountpoint", "/tmp/vz-guest-tools-mount")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("hdiutil attach: %w: %s", err, output)
	}

	mountPoint := "/tmp/vz-guest-tools-mount"
	defer func() {
		exec.Command("hdiutil", "detach", mountPoint, "-force").Run()
	}()

	// Find the .pkg inside the mounted image.
	var pkgSrc string
	entries, err := os.ReadDir(mountPoint)
	if err != nil {
		return fmt.Errorf("read mount point: %w", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".pkg") && !strings.HasPrefix(e.Name(), "._") {
			pkgSrc = filepath.Join(mountPoint, e.Name())
			break
		}
	}
	if pkgSrc == "" {
		return fmt.Errorf("no .pkg found in %s", imgPath)
	}

	// Copy .pkg to cache.
	data, err := os.ReadFile(pkgSrc)
	if err != nil {
		return fmt.Errorf("read pkg: %w", err)
	}
	if len(data) < 100000 {
		return fmt.Errorf("extracted pkg is too small (%d bytes), disk image may be corrupt", len(data))
	}
	if err := os.WriteFile(pkgDest, data, 0644); err != nil {
		return fmt.Errorf("write pkg: %w", err)
	}

	fmt.Printf("  Extracted: %s (%.1f MB)\n", filepath.Base(pkgDest), float64(len(data))/(1024*1024))
	return nil
}

// guestToolsLaunchDaemonPlist runs the guest tools installer on first boot.
// LaunchOnlyOnce prevents re-running after the initial install.
const guestToolsLaunchDaemonPlist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.github.tmc.vz-macos.guest-tools-install</string>
    <key>ProgramArguments</key>
    <array>
        <string>/bin/bash</string>
        <string>/var/db/vz-install-guest-tools.sh</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>LaunchOnlyOnce</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/var/log/vz-guest-tools-install.log</string>
    <key>StandardErrorPath</key>
    <string>/var/log/vz-guest-tools-install.log</string>
</dict>
</plist>
`

// guestToolsInstallScript runs the .pkg installer and self-cleans.
const guestToolsInstallScript = `#!/bin/bash
# Install UTM SPICE guest tools and clean up.
set -e

PKG="/var/db/vz-guest-tools.pkg"
LOG="/var/log/vz-guest-tools-install.log"
MARKER="/var/db/.vz-guest-tools-installed"

log() {
    echo "$(date '+%Y-%m-%d %H:%M:%S') $1" >> "$LOG"
    echo "$1"
}

if [ -f "$MARKER" ]; then
    log "Guest tools already installed, skipping."
    exit 0
fi

if [ ! -f "$PKG" ]; then
    log "ERROR: Package not found: $PKG"
    exit 1
fi

log "Installing SPICE guest tools..."
installer -pkg "$PKG" -target / 2>&1 | tee -a "$LOG"

if [ $? -eq 0 ]; then
    log "Guest tools installed successfully."
    touch "$MARKER"

    # Clean up installer files.
    rm -f "$PKG"
    rm -f /Library/LaunchDaemons/com.github.tmc.vz-macos.guest-tools-install.plist
    rm -f /var/db/vz-install-guest-tools.sh
    log "Installer files cleaned up."
else
    log "ERROR: Installation failed."
    exit 1
fi
`
