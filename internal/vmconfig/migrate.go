package vmconfig

import (
	"fmt"
	"os"
	"path/filepath"
)

// MigrateIfNeeded migrates from the legacy flat VM layout to a default VM.
func MigrateIfNeeded() error {
	baseDir := BaseDir()
	diskPath := filepath.Join(baseDir, "disk.img")
	if _, err := os.Stat(diskPath); os.IsNotExist(err) {
		return nil
	}

	fmt.Println("Migrating VM files to new directory structure...")

	defaultDir := filepath.Join(baseDir, "default")
	if err := os.MkdirAll(defaultDir, 0755); err != nil {
		return fmt.Errorf("create default dir: %w", err)
	}

	for _, name := range Files {
		oldPath := filepath.Join(baseDir, name)
		newPath := filepath.Join(defaultDir, name)
		if _, err := os.Stat(oldPath); err == nil {
			if err := os.Rename(oldPath, newPath); err != nil {
				return fmt.Errorf("move %s: %w", name, err)
			}
			fmt.Printf("  Moved: %s -> default/%s\n", name, name)
		}
	}

	for _, name := range []string{"boot-args.txt"} {
		oldPath := filepath.Join(baseDir, name)
		newPath := filepath.Join(defaultDir, name)
		if _, err := os.Stat(oldPath); err == nil {
			if err := os.Rename(oldPath, newPath); err != nil {
				fmt.Printf("  warning: could not move %s: %v\n", name, err)
			} else {
				fmt.Printf("  Moved: %s -> default/%s\n", name, name)
			}
		}
	}

	cacheDir := CacheDir()
	os.MkdirAll(cacheDir, 0755)
	ipswOld := filepath.Join(baseDir, "RestoreImage.ipsw")
	ipswNew := filepath.Join(cacheDir, "RestoreImage.ipsw")
	if _, err := os.Stat(ipswOld); err == nil {
		if err := os.Rename(ipswOld, ipswNew); err != nil {
			fmt.Printf("  warning: could not move IPSW to cache: %v\n", err)
		} else {
			fmt.Printf("  Moved: RestoreImage.ipsw -> cache/RestoreImage.ipsw\n")
		}
	}

	if err := SetActive("default"); err != nil {
		fmt.Printf("  warning: could not set active VM: %v\n", err)
	}

	fmt.Println("Migration complete. Active VM is now 'default'.")
	return nil
}

// EnsureAlias creates a registry alias for a VM resolved outside BaseDir.
func EnsureAlias(name, resolvedDir string) error {
	if name == "" {
		return nil
	}
	aliasPath := filepath.Join(BaseDir(), name)
	targetPath := resolvePath(resolvedDir)
	if resolvePath(aliasPath) == targetPath {
		return nil
	}
	if _, err := os.Lstat(aliasPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat vm alias %q: %w", aliasPath, err)
	}
	if err := os.MkdirAll(BaseDir(), 0755); err != nil {
		return fmt.Errorf("create vm base dir: %w", err)
	}
	if err := os.Symlink(targetPath, aliasPath); err != nil && !os.IsExist(err) {
		return fmt.Errorf("create vm alias %q -> %q: %w", aliasPath, targetPath, err)
	}
	return nil
}
