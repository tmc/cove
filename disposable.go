package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

const disposableCloneStampFormat = "20060102-150405"

// DisposableClone describes a disposable VM clone created for a session.
type DisposableClone struct {
	Name      string
	Path      string
	Source    string
	CreatedAt time.Time
}

// DisposableSetupOptions configures disposable clone creation.
type DisposableSetupOptions struct {
	Source         string
	Linked         bool
	CopyMachineID  bool
	SourceDiskPath string
	Now            func() time.Time
	Clone          func(CloneOptions) error
}

// DisposableGCOptions configures disposable VM garbage collection.
type DisposableGCOptions struct {
	BaseDir   string
	OlderThan time.Duration
	DryRun    bool
	Now       func() time.Time
	IsActive  func(string) bool
	RemoveAll func(string) error
}

// DisposableGCResult summarizes a garbage-collection run.
type DisposableGCResult struct {
	Scanned      int
	Candidates   int
	SkippedAlive int
	Removed      int
	Paths        []string
}

// disposableCloneName returns a human-readable disposable VM name.
func disposableCloneName(base string, now time.Time) string {
	base = disposableBaseName(base)
	return fmt.Sprintf("%s-d-%s", base, now.Format(disposableCloneStampFormat))
}

// parseDisposableCloneName parses a disposable VM name produced by
// disposableCloneName.
func parseDisposableCloneName(name string) (base string, createdAt time.Time, ok bool) {
	idx := strings.LastIndex(name, "-d-")
	if idx <= 0 {
		return "", time.Time{}, false
	}
	stamp := name[idx+3:]
	if len(stamp) != len(disposableCloneStampFormat) {
		return "", time.Time{}, false
	}
	createdAt, err := time.ParseInLocation(disposableCloneStampFormat, stamp, time.Local)
	if err != nil {
		return "", time.Time{}, false
	}
	base = strings.TrimSpace(name[:idx])
	if base == "" {
		base = "vm"
	}
	return base, createdAt, true
}

// SetupDisposableClone creates a disposable VM clone from source.
func SetupDisposableClone(opts DisposableSetupOptions) (DisposableClone, error) {
	if strings.TrimSpace(opts.Source) == "" {
		return DisposableClone{}, fmt.Errorf("source vm is required")
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	createdAt := now()
	cloneName := disposableCloneName(opts.Source, createdAt)
	cloneFn := opts.Clone
	if cloneFn == nil {
		cloneFn = CloneVM
	}
	cloneOpts := CloneOptions{
		Source:         opts.Source,
		Target:         cloneName,
		Linked:         opts.Linked,
		CopyMachineID:  opts.CopyMachineID,
		SourceDiskPath: opts.SourceDiskPath,
	}
	if err := cloneFn(cloneOpts); err != nil {
		return DisposableClone{}, fmt.Errorf("create disposable clone: %w", err)
	}
	if err := os.Remove(proxyStatePath(vmconfig.Path(cloneName))); err != nil && !os.IsNotExist(err) {
		return DisposableClone{}, fmt.Errorf("clear proxy state from disposable clone: %w", err)
	}
	return DisposableClone{
		Name:      cloneName,
		Path:      vmconfig.Path(cloneName),
		Source:    opts.Source,
		CreatedAt: createdAt,
	}, nil
}

// CleanupDisposableClone removes a disposable clone directory.
func CleanupDisposableClone(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("clone path is required")
	}
	clean := filepath.Clean(path)
	if clean == "." || clean == string(filepath.Separator) {
		return fmt.Errorf("refusing to remove %q", path)
	}
	return os.RemoveAll(path)
}

// GCDisposableClones removes disposable clones older than OlderThan.
func GCDisposableClones(opts DisposableGCOptions) (DisposableGCResult, error) {
	baseDir := opts.BaseDir
	if baseDir == "" {
		baseDir = vmconfig.BaseDir()
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	isActive := opts.IsActive
	if isActive == nil {
		isActive = disposableCloneIsActive
	}
	removeAll := opts.RemoveAll
	if removeAll == nil {
		removeAll = os.RemoveAll
	}

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return DisposableGCResult{}, fmt.Errorf("read vm base dir: %w", err)
	}

	var result DisposableGCResult
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		_, createdAt, ok := parseDisposableCloneName(name)
		if !ok {
			continue
		}
		result.Scanned++
		path := filepath.Join(baseDir, name)
		if isActive(path) {
			result.SkippedAlive++
			continue
		}
		if opts.OlderThan > 0 && now().Sub(createdAt) < opts.OlderThan {
			continue
		}
		result.Candidates++
		result.Paths = append(result.Paths, path)
		if opts.DryRun {
			continue
		}
		if err := removeAll(path); err != nil {
			return result, fmt.Errorf("remove disposable clone %s: %w", path, err)
		}
		result.Removed++
	}

	return result, nil
}

func disposableBaseName(base string) string {
	base = strings.TrimSpace(filepath.Base(base))
	switch base {
	case "", ".", "..", string(filepath.Separator):
		return "vm"
	default:
		return base
	}
}

func disposableCloneIsActive(path string) bool {
	if isVMRunningAt(path) {
		return true
	}
	sock := GetControlSocketPathForVM(path)
	if sock == "" {
		return false
	}
	conn, err := netDialUnixTimeout(sock, 200*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// netDialUnixTimeout is a small indirection so tests can stub it if needed.
var netDialUnixTimeout = func(sock string, timeout time.Duration) (net.Conn, error) {
	conn, err := net.DialTimeout("unix", sock, timeout)
	if err != nil {
		return nil, err
	}
	return conn, nil
}
