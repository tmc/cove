package nixos

import (
	"fmt"
	"net/http"
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

func CachedISOPath(cacheDir string) string {
	return filepath.Join(cacheDir, ISOName)
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
