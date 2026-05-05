package nixos

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
)

const (
	Release       = "25.11"
	Architecture  = "aarch64-linux"
	ISOURL        = "https://channels.nixos.org/nixos-25.11/latest-nixos-minimal-aarch64-linux.iso"
	ISOName       = "nixos-25.11-aarch64-linux.iso"
	MinISOSize    = 1200 * 1024 * 1024
	SeedVolumeID  = "COVE-NIXOS"
	ConfigPath    = "configuration.nix"
	InstallScript = "install-nixos.sh"
)

func DefaultCacheDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "vz"), nil
}

func CachedISOPath(cacheDir string) string {
	return filepath.Join(cacheDir, ISOName)
}

func EnsureISO(cacheDir string) (string, error) {
	if cacheDir == "" {
		var err error
		cacheDir, err = DefaultCacheDir()
		if err != nil {
			return "", fmt.Errorf("cache dir: %w", err)
		}
	}
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", fmt.Errorf("create cache dir: %w", err)
	}
	path := CachedISOPath(cacheDir)
	if info, err := os.Stat(path); err == nil && info.Size() > MinISOSize {
		return path, nil
	}
	return path, DownloadISO(path)
}

func DownloadISO(path string) error {
	cmd := exec.Command("curl", "-L", "-C", "-", "-#", "-o", path, ISOURL)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("download nixos iso: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat nixos iso: %w", err)
	}
	if info.Size() < MinISOSize {
		return fmt.Errorf("nixos iso too small: %d bytes", info.Size())
	}
	return nil
}

func CheckISOURL(client *http.Client) error {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequest(http.MethodHead, ISOURL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("HEAD %s: %s", ISOURL, resp.Status)
	}
	return nil
}
