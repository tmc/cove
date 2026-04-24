package vmconfig

import (
	"os"
	"path/filepath"
)

// HasSuspendState reports whether dir contains suspended VM state.
func HasSuspendState(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "suspend.vmstate"))
	return err == nil
}

// DetectOSType determines the OS type of a VM from layout marker files.
func DetectOSType(dir string) string {
	if _, err := os.Stat(filepath.Join(dir, "hw.model")); err == nil {
		return "macOS"
	}
	if _, err := os.Stat(filepath.Join(dir, "linux-disk.img")); err == nil {
		return "Linux"
	}
	if _, err := os.Stat(filepath.Join(dir, "efi.nvram")); err == nil {
		return "Linux"
	}
	if _, err := os.Stat(filepath.Join(dir, "efi-vars.img")); err == nil {
		return "Linux"
	}
	return "unknown"
}
