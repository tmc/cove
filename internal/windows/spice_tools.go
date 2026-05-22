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
	spiceGuestToolsExeName    = "spice-guest-tools-latest.exe"
	defaultSpiceGuestToolsURL = "https://www.spice-space.org/download/windows/spice-guest-tools/" + spiceGuestToolsExeName
	minSpiceGuestToolsSize    = 100000
)

var spiceGuestToolsURL = defaultSpiceGuestToolsURL

// DefaultWindowsSpiceGuestToolsCacheDir returns the default Windows SPICE tools cache directory.
func DefaultWindowsSpiceGuestToolsCacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home dir: %w", err)
	}
	return filepath.Join(home, ".vz", "windows-guest-tools"), nil
}

// EnsureWindowsSpiceGuestTools returns a cached Windows SPICE guest tools installer path.
func EnsureWindowsSpiceGuestTools(cacheDir string) (string, error) {
	if cacheDir == "" {
		var err error
		cacheDir, err = DefaultWindowsSpiceGuestToolsCacheDir()
		if err != nil {
			return "", err
		}
	}

	exePath := filepath.Join(cacheDir, spiceGuestToolsExeName)
	if info, err := os.Stat(exePath); err == nil && info.Size() >= minSpiceGuestToolsSize {
		return exePath, nil
	}
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", fmt.Errorf("create SPICE guest tools cache dir: %w", err)
	}
	if err := downloadWindowsSpiceGuestTools(exePath); err != nil {
		return "", err
	}
	return exePath, nil
}

func downloadWindowsSpiceGuestTools(path string) error {
	tmp := path + ".tmp"
	if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove partial SPICE guest tools: %w", err)
	}

	client := http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(spiceGuestToolsURL)
	if err != nil {
		return fmt.Errorf("download SPICE guest tools: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download SPICE guest tools: %s", resp.Status)
	}

	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0644)
	if err != nil {
		return fmt.Errorf("create SPICE guest tools: %w", err)
	}
	_, copyErr := io.Copy(f, resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		os.Remove(tmp)
		return fmt.Errorf("write SPICE guest tools: %w", copyErr)
	}
	if closeErr != nil {
		os.Remove(tmp)
		return fmt.Errorf("close SPICE guest tools: %w", closeErr)
	}

	info, err := os.Stat(tmp)
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("stat SPICE guest tools: %w", err)
	}
	if info.Size() < minSpiceGuestToolsSize {
		os.Remove(tmp)
		return fmt.Errorf("SPICE guest tools too small")
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("cache SPICE guest tools: %w", err)
	}
	return nil
}
