//go:build ignore

// windows_drivers.go - Download and cache ARM64 VirtIO driver ISO for Windows VMs.
//
// STATUS: Blocked — see windows.go for details on the GOP/framebuffer issue.
//
// Downloads the lightweight virtiso-arm ISO (~6 MB) from the qemus project,
// which contains only the ARM64 VirtIO drivers needed for Virtualization.framework.
// This is tested and working — the ISO attaches correctly as USB mass storage.
package windows

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const (
	virtioDriversVersion = "0.1.285-1"
	virtioDriversISOName = "virtio-win-0.1.285.iso"
	virtioDriversURL     = "https://github.com/qemus/virtiso-arm/releases/download/v" + virtioDriversVersion + "/" + virtioDriversISOName
)

// windowsDriversCacheDir returns the directory for cached VirtIO driver downloads.
func windowsDriversCacheDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".vz", "windows-drivers")
}

// ensureVirtIODriversISO downloads the VirtIO drivers ISO if not already cached
// and returns the path to the cached file.
func ensureVirtIODriversISO() (string, error) {
	cacheDir := windowsDriversCacheDir()
	isoPath := filepath.Join(cacheDir, virtioDriversISOName)

	// Return cached ISO if it exists and has reasonable size.
	if info, err := os.Stat(isoPath); err == nil && info.Size() > 100000 {
		fmt.Printf("Using cached VirtIO drivers: %s (%.1f MB)\n", isoPath, float64(info.Size())/(1024*1024))
		return isoPath, nil
	}

	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", fmt.Errorf("create cache directory: %w", err)
	}

	fmt.Printf("Downloading VirtIO ARM64 drivers (%s)...\n", virtioDriversVersion)
	if err := downloadVirtIODriversISO(isoPath); err != nil {
		return "", fmt.Errorf("download VirtIO drivers: %w", err)
	}

	return isoPath, nil
}

// downloadVirtIODriversISO downloads the VirtIO drivers ISO using curl.
func downloadVirtIODriversISO(destPath string) error {
	start := time.Now()

	cmd := exec.Command("curl", "-L", "-f", "-#", "-o", destPath, virtioDriversURL)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Printf("  URL: %s\n", virtioDriversURL)

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
