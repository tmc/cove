package vmconfig

import (
	"fmt"
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

// RunsDir returns the per-run artifact bundle root.
// Each `cove run -fork-from` invocation lazily creates a
// <RunsDir()>/<run-id>/ subdirectory holding manifest.json,
// events.jsonl, stdout.log, stderr.log, and screenshots/.
func RunsDir() string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".vz", "runs")
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

// ResolveDir returns the VM directory for vmName or currentDir.
func ResolveDir(vmName, currentDir string) string {
	defaultDir := BaseDir()
	if vmName != "" {
		return Path(vmName)
	}
	if currentDir != "" && currentDir != defaultDir && !IsSubdir(currentDir, defaultDir) {
		return currentDir
	}
	return filepath.Join(BaseDir(), ActiveName())
}

// EnsureDir ensures the resolved VM directory exists and returns its real path.
func EnsureDir(vmName, currentDir string) (string, error) {
	if err := MigrateIfNeeded(); err != nil {
		return "", fmt.Errorf("migration failed: %w", err)
	}
	resolvedDir := ResolveDir(vmName, currentDir)
	if err := os.MkdirAll(resolvedDir, 0755); err != nil {
		return "", fmt.Errorf("create VM dir: %w", err)
	}
	if err := EnsureAlias(vmName, resolvedDir); err != nil {
		return "", err
	}
	return resolvePath(resolvedDir), nil
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
