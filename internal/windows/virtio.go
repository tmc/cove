package windows

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const (
	virtIODriversVersion = "0.1.285-1"
	virtIODriversISOName = "virtio-win-0.1.285.iso"
	virtIODriversURL     = "https://github.com/qemus/virtiso-arm/releases/download/v" + virtIODriversVersion + "/" + virtIODriversISOName
	minVirtIODriversSize = 100000
)

// DefaultVirtIODriversCacheDir returns the default VirtIO driver cache directory.
func DefaultVirtIODriversCacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home dir: %w", err)
	}
	return filepath.Join(home, ".vz", "windows-drivers"), nil
}

// EnsureVirtIODriversISO returns a cached ARM64 VirtIO driver ISO path.
func EnsureVirtIODriversISO(cacheDir string) (string, error) {
	if cacheDir == "" {
		var err error
		cacheDir, err = DefaultVirtIODriversCacheDir()
		if err != nil {
			return "", err
		}
	}

	isoPath := filepath.Join(cacheDir, virtIODriversISOName)
	if info, err := os.Stat(isoPath); err == nil && info.Size() >= minVirtIODriversSize {
		return isoPath, nil
	}
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", fmt.Errorf("create virtio cache dir: %w", err)
	}
	if err := downloadVirtIODriversISO(isoPath); err != nil {
		return "", err
	}
	return isoPath, nil
}

func downloadVirtIODriversISO(path string) error {
	tmp := path + ".tmp"
	if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove partial virtio iso: %w", err)
	}

	client := http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(virtIODriversURL)
	if err != nil {
		return fmt.Errorf("download virtio iso: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download virtio iso: %s", resp.Status)
	}

	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0644)
	if err != nil {
		return fmt.Errorf("create virtio iso: %w", err)
	}
	_, copyErr := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		os.Remove(tmp)
		return fmt.Errorf("write virtio iso: %w", copyErr)
	}
	if closeErr != nil {
		os.Remove(tmp)
		return fmt.Errorf("close virtio iso: %w", closeErr)
	}

	info, err := os.Stat(tmp)
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("stat virtio iso: %w", err)
	}
	if info.Size() < minVirtIODriversSize {
		os.Remove(tmp)
		return fmt.Errorf("virtio iso too small")
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("cache virtio iso: %w", err)
	}
	return nil
}
