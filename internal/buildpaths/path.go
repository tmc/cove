// Package buildpaths contains host path helpers shared by build flows.
package buildpaths

import (
	"os"
	"path/filepath"
	"strings"
)

// ExpandHome expands "~" and "~/" prefixes using the current user's home.
func ExpandHome(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}

// LocalBaseDir reports whether refText names an existing local directory.
func LocalBaseDir(refText string) (string, bool) {
	path := ExpandHome(refText)
	info, err := os.Stat(path)
	return path, err == nil && info.IsDir()
}
