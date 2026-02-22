//go:build ignore

// windows_guest_tools.go - Download and cache SPICE guest tools for Windows VMs.
//
// STATUS: Blocked — see windows.go for details on the GOP/framebuffer issue.
//
// Downloads SPICE Windows guest tools (.exe) for host-guest clipboard sharing
// and display auto-resize. Tested and working — downloads correctly from
// spice-space.org. Can be bundled on autounattend ISO for silent install
// via FirstLogonCommands.
package windows

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const (
	windowsGuestToolsVersion = "0.141"
	windowsGuestToolsExeName = "spice-guest-tools-" + windowsGuestToolsVersion + ".exe"
	windowsGuestToolsURL     = "https://www.spice-space.org/download/windows/spice-guest-tools/spice-guest-tools-" + windowsGuestToolsVersion + "/" + windowsGuestToolsExeName
)

// windowsGuestToolsCacheDir returns the directory for cached Windows guest tools.
func windowsGuestToolsCacheDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".vz", "windows-guest-tools")
}

// ensureWindowsGuestTools downloads the SPICE guest tools .exe if not already
// cached and returns the path to the cached file.
func ensureWindowsGuestTools() (string, error) {
	cacheDir := windowsGuestToolsCacheDir()
	exePath := filepath.Join(cacheDir, windowsGuestToolsExeName)

	// Return cached .exe if it exists and has reasonable size.
	if info, err := os.Stat(exePath); err == nil && info.Size() > 100000 {
		fmt.Printf("Using cached SPICE guest tools: %s (%.1f MB)\n", exePath, float64(info.Size())/(1024*1024))
		return exePath, nil
	}

	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", fmt.Errorf("create cache directory: %w", err)
	}

	fmt.Printf("Downloading Windows SPICE guest tools (%s)...\n", windowsGuestToolsVersion)
	if err := downloadWindowsGuestTools(exePath); err != nil {
		return "", fmt.Errorf("download guest tools: %w", err)
	}

	return exePath, nil
}

// downloadWindowsGuestTools downloads the SPICE guest tools .exe using curl.
func downloadWindowsGuestTools(destPath string) error {
	start := time.Now()

	cmd := exec.Command("curl", "-L", "-f", "-#", "-o", destPath, windowsGuestToolsURL)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Printf("  URL: %s\n", windowsGuestToolsURL)

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
