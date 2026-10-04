package checkpoint

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
	if err := cloneFile(src, dst); err == nil {
		return syncFile(dst)
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func syncFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

func realDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("checkpoint directory is not a real directory: %s", path)
	}
	return nil
}

// Save captures declared files and publishes a verified checkpoint atomically.
func (m *Manager) Save(name, compatibility string, sources []Source) (Manifest, error) {
	manifest := Manifest{Version: Version, Name: name, Created: time.Now().UTC(), Compatibility: compatibility}
	release, err := lockOperation(m.root, true)
	if err != nil {
		return manifest, err
	}
	defer release()
	if err := safeName(name); err != nil {
		return manifest, err
	}
	if compatibility == "" || len(sources) == 0 {
		return manifest, fmt.Errorf("checkpoint needs compatibility and files")
	}
	if pending, err := m.Pending(); err != nil {
		return manifest, err
	} else if pending {
		return manifest, fmt.Errorf("checkpoint restore recovery is required before capture")
	}
	if err := realDirectory(m.root); err != nil {
		return manifest, err
	}
	preflight := map[string]bool{}
	for _, source := range sources {
		if err := safePath(source.Path); err != nil {
			return manifest, err
		}
		if source.Role == "" || preflight[source.Path] {
			return manifest, fmt.Errorf("invalid or repeated checkpoint source %s", source.Path)
		}
		preflight[source.Path] = true
		src := source.Path
		if source.SourcePath != "" {
			src = source.SourcePath
		}
		if _, err := regularFile(m.root, src); err != nil {
			return manifest, err
		}
	}
	parent := filepath.Join(m.root, directory)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return manifest, err
	}
	if err := realDirectory(parent); err != nil {
		return manifest, err
	}
	dest := filepath.Join(parent, name)
	if _, err := os.Lstat(dest); !os.IsNotExist(err) {
		return manifest, fmt.Errorf("checkpoint %q already exists or cannot be inspected", name)
	}
	stage, err := os.MkdirTemp(parent, ".capture-*")
	if err != nil {
		return manifest, err
	}
	defer os.RemoveAll(stage)
	seen := map[string]bool{}
	for _, source := range sources {
		if err := safePath(source.Path); err != nil {
			return manifest, err
		}
		if source.Role == "" || seen[source.Path] {
			return manifest, fmt.Errorf("invalid or repeated checkpoint source %s", source.Path)
		}
		seen[source.Path] = true
		src := source.Path
		if source.SourcePath != "" {
			src = source.SourcePath
		}
		size, digest, err := hashFile(m.root, src)
		if err != nil {
			return manifest, err
		}
		entry := Entry{Source: Source{Path: source.Path, Role: source.Role}, Size: size, SHA256: digest}
		if err := m.check("capture-copy:" + source.Path); err != nil {
			return manifest, err
		}
		if err := copyFile(filepath.Join(m.root, src), filepath.Join(stage, source.Path)); err != nil {
			return manifest, fmt.Errorf("capture %s: %w", src, err)
		}
		if err := verifyEntry(stage, entry); err != nil {
			return manifest, err
		}
		if err := verifyEntry(m.root, Entry{Source: Source{Path: src, Role: source.Role}, Size: size, SHA256: digest}); err != nil {
			return manifest, err
		}
		manifest.Files = append(manifest.Files, entry)
	}
	if err := validateManifest(manifest); err != nil {
		return manifest, err
	}
	if err := m.check("capture-manifest"); err != nil {
		return manifest, err
	}
	if err := writeJSON(filepath.Join(stage, "manifest.json"), manifest); err != nil {
		return manifest, err
	}
	if err := syncTree(stage); err != nil {
		return manifest, err
	}
	if err := m.check("capture-publish"); err != nil {
		return manifest, err
	}
	if err := publish(stage, dest); err != nil {
		return manifest, err
	}
	if err := syncDir(parent); err != nil {
		return manifest, err
	}
	return manifest, nil
}

func syncTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return syncDir(path)
		}
		return nil
	})
}

// Read verifies a checkpoint manifest and every declared file.
func (m *Manager) Read(name string) (Manifest, error) {
	var manifest Manifest
	if err := safeName(name); err != nil {
		return manifest, err
	}
	parent := filepath.Join(m.root, directory)
	if err := realDirectory(m.root); err != nil {
		return manifest, err
	}
	if err := realDirectory(parent); err != nil {
		return manifest, err
	}
	root := filepath.Join(parent, name)
	if err := realDirectory(root); err != nil {
		return manifest, err
	}
	info, err := os.Lstat(filepath.Join(root, "manifest.json"))
	if err != nil {
		return manifest, err
	}
	if !info.Mode().IsRegular() {
		return manifest, fmt.Errorf("checkpoint manifest is not a regular file")
	}
	data, err := readMetadata(filepath.Join(root, "manifest.json"))
	if err != nil {
		return manifest, err
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, err
	}
	if manifest.Name != name {
		return manifest, fmt.Errorf("checkpoint manifest name mismatch")
	}
	if err := validateManifest(manifest); err != nil {
		return manifest, err
	}
	for _, entry := range manifest.Files {
		if err := verifyEntry(root, entry); err != nil {
			return manifest, err
		}
	}
	return manifest, nil
}
