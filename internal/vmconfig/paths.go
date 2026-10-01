package vmconfig

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tmc/cove/internal/mutationguard"
)

const StateDirEnv = "COVE_STATE_DIR"

// StateDir returns the root directory for cove state.
func StateDir() string {
	if dir := strings.TrimSpace(os.Getenv(StateDirEnv)); dir != "" {
		if abs, err := filepath.Abs(dir); err == nil {
			return abs
		}
		return filepath.Clean(dir)
	}
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".vz")
}

// BaseDir returns the base directory for all VMs.
func BaseDir() string {
	return filepath.Join(StateDir(), "vms")
}

// TemplateDir returns the directory for templates.
func TemplateDir() string {
	return filepath.Join(StateDir(), "templates")
}

// BundleDir returns the directory for Finder-openable VM package aliases.
func BundleDir() string {
	return filepath.Join(StateDir(), "covevms")
}

// CacheDir returns the cache directory.
func CacheDir() string {
	return filepath.Join(StateDir(), "cache")
}

// RunsDir returns the per-run artifact bundle root.
// Each `cove run -fork-from` invocation lazily creates a
// <RunsDir()>/<run-id>/ subdirectory holding manifest.json,
// events.jsonl, stdout.log, stderr.log, and screenshots/.
func RunsDir() string {
	return filepath.Join(StateDir(), "runs")
}

// CurrentLink returns the path to the current VM symlink.
func CurrentLink() string {
	return filepath.Join(StateDir(), "current")
}

// Path returns the path to a VM by name.
func Path(name string) string {
	if existing, ok := ExistingPath(name); ok {
		return existing
	}
	return filepath.Join(BaseDir(), PackageName(name))
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
	packageName := PackageName(name)
	return []string{
		filepath.Join(baseDir, name),
		filepath.Join(baseDir, packageName),
		filepath.Join(homeDir, name),
		filepath.Join(homeDir, packageName),
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	guard, err := mutationguard.AcquireContext(ctx, StateDir())
	if err != nil {
		return "", err
	}
	defer guard.Release()
	return EnsureDirWithGuard(vmName, currentDir, guard)
}

// EnsureDirWithGuard ensures a VM directory while the caller holds its root guard.
func EnsureDirWithGuard(vmName, currentDir string, guard *mutationguard.Guard) (string, error) {
	if err := guard.Check(StateDir()); err != nil {
		return "", err
	}
	return ensureDirLocked(vmName, currentDir)
}

func ensureDirLocked(vmName, currentDir string) (string, error) {
	if err := migrateIfNeededLocked(); err != nil {
		return "", fmt.Errorf("migration failed: %w", err)
	}
	resolvedDir := ResolveDir(vmName, currentDir)
	if target, dangling := danglingLink(resolvedDir); dangling {
		return "", fmt.Errorf("VM %q is registered but its bundle is missing: %s points at %s, which does not exist\n  list VMs: cove list\n  remove the stale entry: cove rm %s",
			filepath.Base(resolvedDir), resolvedDir, target, filepath.Base(resolvedDir))
	}
	if err := os.MkdirAll(resolvedDir, 0755); err != nil {
		// A concurrent cove invocation (e.g. two terminals racing on the
		// same VM name) can win the mkdir between our dangling-link check
		// and this call. If the path is a real directory now, treat it as
		// success instead of failing the whole command.
		info, statErr := os.Stat(resolvedDir)
		if statErr != nil || !info.IsDir() {
			return "", fmt.Errorf("create VM dir: %w", err)
		}
	}
	if filepath.Base(resolvedDir) == PackageName(filepath.Base(resolvedDir)) {
		if err := markFinderPackage(resolvedDir); err != nil {
			return "", err
		}
	}
	if err := ensureAliasLocked(vmName, resolvedDir); err != nil {
		return "", err
	}
	if vmName != "" {
		if err := ensureCompatibilityAliasLocked(vmName, resolvedDir); err != nil {
			return "", err
		}
	}
	return resolvePath(resolvedDir), nil
}

// DanglingLink reports whether path is a symlink whose target does not exist,
// and returns the target it names. os.MkdirAll on such a path fails with
// EEXIST ("file exists") because the link itself is present while Stat fails,
// which reads as a confusing error unless the caller names the real cause.
// A target that cannot be checked (for example, permission denied) is not
// reported as dangling.
func DanglingLink(path string) (string, bool) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return "", false
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		return "", false
	}
	target, err := os.Readlink(path)
	if err != nil {
		return "", true
	}
	return target, true
}

func danglingLink(path string) (string, bool) {
	return DanglingLink(path)
}

// DanglingSymlink represents a symlink whose target does not exist.
type DanglingSymlink struct {
	Path   string
	Target string

	// Volume is set when Target lies on a volume under /Volumes that
	// is not mounted. The VM is then likely intact on a detached disk,
	// and the link should be kept.
	Volume string
}

// DanglingSymlinks returns all dangling symlinks found directly under dirs.
func DanglingSymlinks(dirs ...string) []DanglingSymlink {
	var list []DanglingSymlink
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			p := filepath.Join(dir, entry.Name())
			if target, ok := DanglingLink(p); ok {
				list = append(list, DanglingSymlink{
					Path:   p,
					Target: target,
					Volume: unmountedVolume(target),
				})
			}
		}
	}
	return list
}

// unmountedVolume returns the /Volumes/<name> root of target if that
// volume is not mounted, or "" otherwise.
func unmountedVolume(target string) string {
	rest, ok := strings.CutPrefix(filepath.Clean(target), "/Volumes/")
	if !ok {
		return ""
	}
	name, _, _ := strings.Cut(rest, "/")
	if name == "" {
		return ""
	}
	vol := "/Volumes/" + name
	if _, err := os.Stat(vol); errors.Is(err, fs.ErrNotExist) {
		return vol
	}
	return ""
}

// DanglingVMSymlinks returns all dangling symlinks in BaseDir (~/.vz/vms)
// and BundleDir (~/.vz/covevms).
func DanglingVMSymlinks() []DanglingSymlink {
	return DanglingSymlinks(BaseDir(), BundleDir())
}

// RemoveDanglingSymlinks removes the links in list that are still dangling.
// Links whose target is on an unmounted volume are skipped.
func RemoveDanglingSymlinks(list []DanglingSymlink) ([]DanglingSymlink, error) {
	var removed []DanglingSymlink
	var firstErr error
	for _, sym := range list {
		if sym.Volume != "" {
			continue
		}
		if _, ok := DanglingLink(sym.Path); !ok {
			continue
		}
		if err := os.Remove(sym.Path); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("remove dangling symlink %s: %w", sym.Path, err)
			}
		} else {
			removed = append(removed, sym)
		}
	}
	return removed, firstErr
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
