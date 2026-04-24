// vm_registry.go - VM discovery and management
package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// applyVMConfig loads saved VM config and applies defaults for flags
// not explicitly set by the user. When the user does pass -cpu or -memory,
// the new value is saved to config.json for subsequent boots.
func applyVMConfig(dir string) {
	cfg, err := LoadVMConfig(dir)
	if err != nil {
		return
	}

	// Track which flags the user explicitly set
	cpuSet, memSet := false, false
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "cpu":
			cpuSet = true
		case "memory":
			memSet = true
		}
	})

	changed := false

	if !cpuSet && cfg.CPU > 0 {
		cpuCount = cfg.CPU
	} else if cpuSet && cfg.CPU != cpuCount {
		cfg.CPU = cpuCount
		changed = true
	}

	if !memSet && cfg.MemoryGB > 0 {
		memoryGB = cfg.MemoryGB
	} else if memSet && cfg.MemoryGB != memoryGB {
		cfg.MemoryGB = memoryGB
		changed = true
	}

	if changed {
		if err := SaveVMConfig(dir, cfg); err != nil {
			fmt.Printf("warning: save vm config: %v\n", err)
		}
	}
}

// savePostInstallRecipes persists the selected post-install recipes
// so they can be retried if installation or scripting fails.
func savePostInstallRecipes(dir, recipes string) {
	cfg, err := LoadVMConfig(dir)
	if err != nil {
		cfg = &VMConfig{}
	}
	cfg.PostInstallRecipes = recipes
	if err := SaveVMConfig(dir, cfg); err != nil {
		fmt.Printf("warning: save vzscript config: %v\n", err)
	}
}

// saveHardwareConfig persists the current CPU and memory settings.
func saveHardwareConfig(dir string) {
	cfg, err := LoadVMConfig(dir)
	if err != nil {
		return
	}
	if cfg.CPU == cpuCount && cfg.MemoryGB == memoryGB {
		return
	}
	cfg.CPU = cpuCount
	cfg.MemoryGB = memoryGB
	if err := SaveVMConfig(dir, cfg); err != nil {
		fmt.Printf("warning: save vm config: %v\n", err)
	}
}

// VMInfo holds information about a virtual machine.
type VMInfo struct {
	Name     string    // VM name (directory name)
	Path     string    // Full path to VM directory
	DiskSize int64     // Disk image size in bytes (sparse size)
	Created  time.Time // Creation time (from disk.img mtime)
	State    string    // "running", "stopped", or "suspended"
	OSType   string    // "macOS", "Linux", or "unknown"
}

// GetVMInfo returns information about a specific VM.
func GetVMInfo(vmPath string) (*VMInfo, error) {
	if !ValidateVM(vmPath) {
		return nil, fmt.Errorf("invalid VM: %s", vmPath)
	}

	name := filepath.Base(vmPath)
	diskPath := filepath.Join(vmPath, "disk.img")
	// Fall back to linux-disk.img for Linux VMs
	if _, err := os.Stat(diskPath); os.IsNotExist(err) {
		diskPath = filepath.Join(vmPath, "linux-disk.img")
	}

	// Get disk info
	diskInfo, err := os.Stat(diskPath)
	if err != nil {
		return nil, fmt.Errorf("stat disk.img: %w", err)
	}

	return &VMInfo{
		Name:     name,
		Path:     vmPath,
		DiskSize: diskInfo.Size(),
		Created:  diskInfo.ModTime(),
		State:    detectVMState(vmPath),
		OSType:   detectOSType(vmPath),
	}, nil
}

func detectVMState(vmPath string) string {
	if isVMRunningAt(vmPath) {
		return "running"
	}
	if hasSuspendStateAt(vmPath) {
		return "suspended"
	}
	return "stopped"
}

func hasSuspendStateAt(vmPath string) bool {
	_, err := os.Stat(filepath.Join(vmPath, "suspend.vmstate"))
	return err == nil
}

func isVMRunningAt(vmPath string) bool {
	sock := GetControlSocketPathForVM(vmPath)
	conn, err := net.DialTimeout("unix", sock, 200*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// ListVMs returns all VMs in the base directory.
func ListVMs() ([]VMInfo, error) {
	baseDir := GetVMBaseDir()

	// Create base directory if it doesn't exist
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("create base dir: %w", err)
	}

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil, fmt.Errorf("read base dir: %w", err)
	}

	var vms []VMInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		vmPath := filepath.Join(baseDir, entry.Name())
		info, err := GetVMInfo(vmPath)
		if err != nil {
			continue // Skip invalid VMs
		}

		vms = append(vms, *info)
	}

	// Sort by name
	sort.Slice(vms, func(i, j int) bool {
		return vms[i].Name < vms[j].Name
	})

	return vms, nil
}

// ResolveVMDir returns the VM directory to use.
// If a specific VM name is given (via -vm flag), use that.
// Otherwise, use the vmDir flag value or the active VM.
func ResolveVMDir(vmName string) string {
	// If vmDir is explicitly set to something other than default, use it directly
	homeDir, _ := os.UserHomeDir()
	defaultVMDir := filepath.Join(homeDir, ".vz", "vms")

	// If vmName is specified, use it
	if vmName != "" {
		return GetVMPath(vmName)
	}

	// If vmDir is not the default, use it directly (backwards compatibility)
	if vmDir != "" && vmDir != defaultVMDir && !isSubdir(vmDir, defaultVMDir) {
		return vmDir
	}

	// Use active VM or default to "default"
	activeVM := GetActiveVM()
	return filepath.Join(GetVMBaseDir(), activeVM)
}

// FormatSize formats bytes as human-readable size.
func FormatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// EnsureVMDir ensures the VM directory exists and runs migration if needed.
func EnsureVMDir(vmName string) (string, error) {
	// Run migration first
	if err := MigrateIfNeeded(); err != nil {
		return "", fmt.Errorf("migration failed: %w", err)
	}

	// Resolve the VM directory
	resolvedDir := ResolveVMDir(vmName)

	// Create if it doesn't exist
	if err := os.MkdirAll(resolvedDir, 0755); err != nil {
		return "", fmt.Errorf("create VM dir: %w", err)
	}

	if err := ensureVMAlias(vmName, resolvedDir); err != nil {
		return "", err
	}

	return resolvePath(resolvedDir), nil
}

// detectOSType determines the OS type of a VM by checking for characteristic files.
// hw.model → macOS, efi.nvram/efi-vars.img → Linux, otherwise unknown.
func detectOSType(vmPath string) string {
	if _, err := os.Stat(filepath.Join(vmPath, "hw.model")); err == nil {
		return "macOS"
	}
	if _, err := os.Stat(filepath.Join(vmPath, "linux-disk.img")); err == nil {
		return "Linux"
	}
	if _, err := os.Stat(filepath.Join(vmPath, "efi.nvram")); err == nil {
		return "Linux"
	}
	if _, err := os.Stat(filepath.Join(vmPath, "efi-vars.img")); err == nil {
		return "Linux"
	}
	return "unknown"
}
