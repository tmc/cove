package vmconfig

import (
	"os"
	"path/filepath"
)

// BaseDir returns the base directory for all VMs.
func BaseDir() string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".vz", "vms")
}

// TemplateDir returns the directory for templates.
func TemplateDir() string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".vz", "templates")
}

// CacheDir returns the cache directory.
func CacheDir() string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".vz", "cache")
}

// CurrentLink returns the path to the current VM symlink.
func CurrentLink() string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".vz", "current")
}

// Path returns the path to a VM by name.
func Path(name string) string {
	if existing, ok := ExistingPath(name); ok {
		return existing
	}
	return filepath.Join(BaseDir(), name)
}

// ExistingPath returns an existing registered or legacy VM path by name.
func ExistingPath(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	for _, candidate := range PathCandidates(name) {
		info, err := os.Stat(candidate)
		if err != nil || !info.IsDir() {
			continue
		}
		return resolvePath(candidate), true
	}
	return "", false
}

// PathCandidates returns registered and legacy VM path candidates by name.
func PathCandidates(name string) []string {
	baseDir := BaseDir()
	homeDir := filepath.Dir(baseDir)
	return []string{
		filepath.Join(baseDir, name),
		filepath.Join(homeDir, name),
	}
}

// IsSubdir reports whether path is below base.
func IsSubdir(path, base string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return rel != ".." && !filepath.IsAbs(rel) && rel[0] != '.'
}

func resolvePath(path string) string {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return absPath
	}
	return realPath
}
