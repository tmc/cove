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

	defaultDir := filepath.Join(baseDir, PackageName("default"))
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
	if packaged, err := EnsurePackageLayout("default", defaultDir); err != nil {
		fmt.Printf("  warning: could not create Finder VM package: %v\n", err)
	} else if err := EnsurePackageAlias("default", packaged); err != nil {
		fmt.Printf("  warning: could not create Finder VM package alias: %v\n", err)
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

// EnsurePackageLayout renames a registered VM directory to a .covevm package
// and leaves a compatibility symlink at the old path.
func EnsurePackageLayout(name, resolvedDir string) (string, error) {
	if name == "" || resolvedDir == "" {
		return resolvedDir, nil
	}
	targetPath := resolvePath(resolvedDir)
	if filepath.Base(targetPath) == PackageName(filepath.Base(targetPath)) {
		if err := markFinderPackage(targetPath); err != nil {
			return "", err
		}
		if err := EnsureCompatibilityAlias(name, targetPath); err != nil {
			return "", err
		}
		return targetPath, nil
	}
	baseDir := resolvePath(BaseDir())
	if filepath.Dir(targetPath) != baseDir {
		return targetPath, nil
	}
	packagePath := filepath.Join(baseDir, PackageName(name))
	if _, err := os.Lstat(packagePath); err == nil {
		return targetPath, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("stat vm package dir %q: %w", packagePath, err)
	}
	if err := os.Rename(targetPath, packagePath); err != nil {
		return "", fmt.Errorf("rename vm package dir %q -> %q: %w", targetPath, packagePath, err)
	}
	if err := markFinderPackage(packagePath); err != nil {
		return "", err
	}
	if err := EnsureCompatibilityAlias(name, packagePath); err != nil {
		return "", err
	}
	return resolvePath(packagePath), nil
}

// EnsureCompatibilityAlias leaves the old extensionless VM path usable.
func EnsureCompatibilityAlias(name, packageDir string) error {
	if name == "" || name == PackageName(name) {
		return nil
	}
	aliasPath := filepath.Join(BaseDir(), name)
	targetPath := resolvePath(packageDir)
	if resolvePath(aliasPath) == targetPath {
		return nil
	}
	if info, err := os.Lstat(aliasPath); err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		link, err := os.Readlink(aliasPath)
		if err != nil {
			return fmt.Errorf("read vm compatibility alias %q: %w", aliasPath, err)
		}
		if resolvePath(link) == targetPath {
			return nil
		}
		if err := os.Remove(aliasPath); err != nil {
			return fmt.Errorf("replace vm compatibility alias %q: %w", aliasPath, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat vm compatibility alias %q: %w", aliasPath, err)
	}
	if err := os.MkdirAll(BaseDir(), 0755); err != nil {
		return fmt.Errorf("create vm base dir: %w", err)
	}
	if err := os.Symlink(targetPath, aliasPath); err != nil && !os.IsExist(err) {
		return fmt.Errorf("create vm compatibility alias %q -> %q: %w", aliasPath, targetPath, err)
	}
	return nil
}

// RemoveCompatibilityAlias removes the extensionless VM compatibility symlink.
func RemoveCompatibilityAlias(name string) error {
	aliasPath := filepath.Join(BaseDir(), name)
	info, err := os.Lstat(aliasPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat vm compatibility alias %q: %w", aliasPath, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return nil
	}
	if err := os.Remove(aliasPath); err != nil {
		return fmt.Errorf("remove vm compatibility alias %q: %w", aliasPath, err)
	}
	return nil
}

// EnsurePackageAlias creates a Finder-openable .covevm alias for a VM.
func EnsurePackageAlias(name, resolvedDir string) error {
	if name == "" || resolvedDir == "" {
		return nil
	}
	targetPath := resolvePath(resolvedDir)
	if !Validate(targetPath) {
		return nil
	}
	aliasPath := PackageAliasPath(name)
	if resolvePath(aliasPath) == targetPath {
		return nil
	}
	if info, err := os.Lstat(aliasPath); err == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		link, err := os.Readlink(aliasPath)
		if err != nil {
			return fmt.Errorf("read vm package alias %q: %w", aliasPath, err)
		}
		if resolvePath(link) == targetPath {
			return nil
		}
		if err := os.Remove(aliasPath); err != nil {
			return fmt.Errorf("replace vm package alias %q: %w", aliasPath, err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat vm package alias %q: %w", aliasPath, err)
	}
	if err := os.MkdirAll(BundleDir(), 0755); err != nil {
		return fmt.Errorf("create vm package alias dir: %w", err)
	}
	if err := os.Symlink(targetPath, aliasPath); err != nil && !os.IsExist(err) {
		return fmt.Errorf("create vm package alias %q -> %q: %w", aliasPath, targetPath, err)
	}
	return nil
}

func PackageName(name string) string {
	if filepath.Ext(name) == ".covevm" {
		return name
	}
	return name + ".covevm"
}

// EnsurePackageAliases creates Finder-openable .covevm aliases for VMs.
func EnsurePackageAliases(vms []Info) error {
	for _, vm := range vms {
		if err := EnsurePackageAlias(vm.Name, vm.Path); err != nil {
			return err
		}
	}
	return nil
}

// PackageAliasPath returns the Finder-openable package alias path for name.
func PackageAliasPath(name string) string {
	return filepath.Join(BundleDir(), PackageName(name))
}

// RemovePackageAlias removes the Finder-openable .covevm alias for name.
func RemovePackageAlias(name string) error {
	aliasPath := PackageAliasPath(name)
	info, err := os.Lstat(aliasPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat vm package alias %q: %w", aliasPath, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return nil
	}
	if err := os.Remove(aliasPath); err != nil {
		return fmt.Errorf("remove vm package alias %q: %w", aliasPath, err)
	}
	return nil
}
