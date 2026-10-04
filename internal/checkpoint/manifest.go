// Package checkpoint captures declared VM files and restores them with a
// recoverable transaction. Callers must hold exclusive VM ownership and keep
// its files quiescent throughout capture, restore, and recovery.
package checkpoint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Version is the supported checkpoint manifest version.
const Version = 1
const directory = "checkpoints"
const transactionDirectory = ".checkpoint-restore"

// Source names a bundle-relative file and its role in the checkpoint.
type Source struct {
	Path string `json:"path"`
	Role string `json:"role"`
	// SourcePath selects a temporary captured source; Path remains the restore target.
	SourcePath string `json:"-"`
}

// Entry records the verified bytes of one captured file.
type Entry struct {
	Source
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Manifest lists all files required by a checkpoint.
type Manifest struct {
	Version       int       `json:"version"`
	Name          string    `json:"name"`
	Created       time.Time `json:"created"`
	Compatibility string    `json:"compatibility"`
	Files         []Entry   `json:"files"`
}

// Manager manages one VM bundle. Its zero value is not usable; use New.
type Manager struct {
	root  string
	fault func(string) error
}

// New returns a manager for a VM bundle directory.
func New(root string) *Manager { return &Manager{root: root} }

func safeName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") || strings.HasPrefix(name, ".") {
		return fmt.Errorf("invalid checkpoint name %q", name)
	}
	return nil
}

func safePath(path string) error {
	if path == "" || filepath.IsAbs(path) || filepath.Clean(path) != path || path == "." || strings.Contains(path, "\\") || strings.HasPrefix(path, "../") {
		return fmt.Errorf("invalid checkpoint file path %q", path)
	}
	first := strings.Split(path, string(filepath.Separator))[0]
	if first == directory || first == transactionDirectory || first == "run.lock" || first == "manifest.json" || strings.HasPrefix(first, ".") {
		return fmt.Errorf("unsupported checkpoint file path %q", path)
	}
	return nil
}

func regularFile(root, path string) (os.FileInfo, error) {
	if err := safePath(path); err != nil {
		return nil, err
	}
	parts := strings.Split(path, string(filepath.Separator))
	current := root
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("checkpoint root is not a real directory")
	}
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("checkpoint path contains a symlink: %s", path)
		}
		if i < len(parts)-1 {
			if !info.IsDir() {
				return nil, fmt.Errorf("checkpoint path parent is not a directory: %s", path)
			}
		} else {
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("checkpoint file is not regular: %s", path)
			}
			return info, nil
		}
	}
	return nil, fmt.Errorf("invalid checkpoint path %q", path)
}

func hashFile(root, path string) (int64, string, error) {
	before, err := regularFile(root, path)
	if err != nil {
		return 0, "", err
	}
	f, err := os.Open(filepath.Join(root, path))
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return 0, "", fmt.Errorf("checkpoint source changed while opening: %s", path)
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, "", err
	}
	after, err := regularFile(root, path)
	if err != nil {
		return 0, "", err
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) || n != before.Size() {
		return 0, "", fmt.Errorf("checkpoint source changed while reading: %s", path)
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

func verifyEntry(root string, entry Entry) error {
	size, digest, err := hashFile(root, entry.Path)
	if err != nil {
		return fmt.Errorf("verify %s: %w", entry.Path, err)
	}
	if size != entry.Size || digest != entry.SHA256 {
		return fmt.Errorf("checkpoint file %s content does not match manifest", entry.Path)
	}
	return nil
}

func validateManifest(manifest Manifest) error {
	if manifest.Version != Version {
		return fmt.Errorf("unsupported checkpoint manifest version %d", manifest.Version)
	}
	if err := safeName(manifest.Name); err != nil {
		return err
	}
	if manifest.Compatibility == "" || len(manifest.Files) == 0 {
		return fmt.Errorf("checkpoint manifest lacks compatibility or files")
	}
	seen := map[string]bool{}
	for _, entry := range manifest.Files {
		if err := safePath(entry.Path); err != nil {
			return err
		}
		if seen[entry.Path] {
			return fmt.Errorf("duplicate checkpoint path %s", entry.Path)
		}
		seen[entry.Path] = true
		if entry.Role == "" || entry.Size < 0 || len(entry.SHA256) != 64 {
			return fmt.Errorf("invalid checkpoint entry %s", entry.Path)
		}
		if _, err := hex.DecodeString(entry.SHA256); err != nil {
			return fmt.Errorf("invalid checkpoint digest: %w", err)
		}
	}
	return nil
}

func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".checkpoint-json-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

func (m *Manager) check(point string) error {
	if m.fault != nil {
		return m.fault(point)
	}
	return nil
}
