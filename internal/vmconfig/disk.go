package vmconfig

import (
	"os"
	"path/filepath"
)

var windowsDiskNames = []string{
	"windows-disk.img",
	"windows.qcow2",
}

func existingWindowsDiskPath(dir string) (string, bool) {
	for _, name := range windowsDiskNames {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			return path, true
		}
	}
	return "", false
}

// WindowsDiskPath returns the existing Windows disk path for dir, or the
// default Virtualization.framework Windows disk path if none exists yet.
func WindowsDiskPath(dir string) string {
	if path, ok := existingWindowsDiskPath(dir); ok {
		return path
	}
	return filepath.Join(dir, "windows-disk.img")
}
