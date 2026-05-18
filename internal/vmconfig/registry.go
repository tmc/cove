package vmconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Files are the files that make up a macOS VM.
var Files = []string{
	"disk.img",
	"aux.img",
	"hw.model",
	"machine.id",
}

// RequiredFiles are the minimum files needed for a valid macOS VM.
var RequiredFiles = []string{
	"disk.img",
	"aux.img",
}

// Validate checks whether dir contains a valid VM.
func Validate(dir string) bool {
	macOSValid := true
	for _, name := range RequiredFiles {
		if _, err := os.Stat(filepath.Join(dir, name)); os.IsNotExist(err) {
			macOSValid = false
			break
		}
	}
	if macOSValid {
		return true
	}
	if _, err := os.Stat(filepath.Join(dir, "linux-disk.img")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(dir, "windows-disk.img")); err == nil {
		return true
	}
	return false
}

// ListOrphans returns directory names under BaseDir that are not valid VMs.
func ListOrphans() ([]string, error) {
	baseDir := BaseDir()
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read base dir: %w", err)
	}
	var orphans []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		vmPath := filepath.Join(baseDir, entry.Name())
		if Validate(vmPath) {
			continue
		}
		orphans = append(orphans, entry.Name())
	}
	sort.Strings(orphans)
	return orphans, nil
}

// ActiveName returns the name of the currently active VM.
func ActiveName() string {
	target, err := os.Readlink(CurrentLink())
	if err != nil {
		return "default"
	}
	return NameForPath(target)
}

// SetActive sets the active VM symlink.
func SetActive(name string) error {
	vmPath := Path(name)
	if !Validate(vmPath) {
		return fmt.Errorf("vm not found or invalid: %s", name)
	}
	linkPath := CurrentLink()
	os.Remove(linkPath)
	if err := os.Symlink(vmPath, linkPath); err != nil {
		return fmt.Errorf("create symlink: %w", err)
	}
	return nil
}

// UnsetActive removes the active VM symlink.
func UnsetActive() error {
	if err := os.Remove(CurrentLink()); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("remove active VM link: %w", err)
	}
	return nil
}
