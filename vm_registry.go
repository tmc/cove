// vm_registry.go - VM discovery and management
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// VMConfig holds persistent configuration for a VM.
// Stored as config.json in the VM directory.
type VMConfig struct {
	CPU                uint           `json:"cpu,omitempty"`
	MemoryGB           uint64         `json:"memoryGB,omitempty"`
	Volumes            []VolumeMount  `json:"volumes,omitempty"`
	PostInstallRecipes string         `json:"postInstallRecipes,omitempty"`
	Agent              *VMAgentConfig `json:"agent,omitempty"`
}

// LoadVMConfig reads the VM configuration from vmDir/config.json.
// Returns an empty config (not an error) if the file does not exist.
func LoadVMConfig(vmDir string) (*VMConfig, error) {
	path := filepath.Join(vmDir, "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &VMConfig{}, nil
		}
		return nil, fmt.Errorf("read vm config: %w", err)
	}
	var cfg VMConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse vm config: %w", err)
	}
	return &cfg, nil
}

// SaveVMConfig writes the VM configuration to vmDir/config.json.
func SaveVMConfig(vmDir string, cfg *VMConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal vm config: %w", err)
	}
	path := filepath.Join(vmDir, "config.json")
	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("write vm config: %w", err)
	}
	return nil
}

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

// VMFiles are the files that make up a VM.
var VMFiles = []string{
	"disk.img",
	"aux.img",
	"hw.model",
	"machine.id",
}

// VMFilesRequired are the minimum files needed for a valid VM.
var VMFilesRequired = []string{
	"disk.img",
	"aux.img",
}

// GetVMBaseDir returns the base directory for all VMs (~/.vz/vms).
func GetVMBaseDir() string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".vz", "vms")
}

// GetTemplateDir returns the directory for templates (~/.vz/templates).
func GetTemplateDir() string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".vz", "templates")
}

// GetCacheDir returns the cache directory (~/.vz/cache).
func GetCacheDir() string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".vz", "cache")
}

// GetCurrentVMLink returns the path to the current VM symlink (~/.vz/current).
func GetCurrentVMLink() string {
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, ".vz", "current")
}

// ValidateVM checks if a directory contains a valid VM.
// Accepts either macOS files (disk.img + aux.img) or Linux files (linux-disk.img).
func ValidateVM(vmPath string) bool {
	// macOS VM: disk.img + aux.img
	macOSValid := true
	for _, f := range VMFilesRequired {
		if _, err := os.Stat(filepath.Join(vmPath, f)); os.IsNotExist(err) {
			macOSValid = false
			break
		}
	}
	if macOSValid {
		return true
	}
	// Linux VM: linux-disk.img is sufficient
	if _, err := os.Stat(filepath.Join(vmPath, "linux-disk.img")); err == nil {
		return true
	}
	return false
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

// GetActiveVM returns the name of the currently active VM.
func GetActiveVM() string {
	linkPath := GetCurrentVMLink()
	target, err := os.Readlink(linkPath)
	if err != nil {
		return "default"
	}
	return filepath.Base(target)
}

// SetActiveVM sets the active VM by updating the symlink.
func SetActiveVM(name string) error {
	vmPath := filepath.Join(GetVMBaseDir(), name)
	if !ValidateVM(vmPath) {
		return fmt.Errorf("vm not found or invalid: %s", name)
	}

	linkPath := GetCurrentVMLink()

	// Remove existing symlink if it exists
	os.Remove(linkPath)

	// Create new symlink
	if err := os.Symlink(vmPath, linkPath); err != nil {
		return fmt.Errorf("create symlink: %w", err)
	}

	return nil
}

// GetVMPath returns the path to a VM by name.
func GetVMPath(name string) string {
	return filepath.Join(GetVMBaseDir(), name)
}

// MigrateIfNeeded migrates from flat structure to new multi-VM structure.
// Old: ~/.vz/vms/disk.img, ~/.vz/vms/aux.img, etc.
// New: ~/.vz/vms/default/disk.img, ~/.vz/vms/default/aux.img, etc.
func MigrateIfNeeded() error {
	baseDir := GetVMBaseDir()

	// Check if migration is needed (flat files exist in base dir)
	diskPath := filepath.Join(baseDir, "disk.img")
	if _, err := os.Stat(diskPath); os.IsNotExist(err) {
		return nil // No migration needed
	}

	fmt.Println("Migrating VM files to new directory structure...")

	// Create default VM directory
	defaultDir := filepath.Join(baseDir, "default")
	if err := os.MkdirAll(defaultDir, 0755); err != nil {
		return fmt.Errorf("create default dir: %w", err)
	}

	// Move files
	for _, f := range VMFiles {
		oldPath := filepath.Join(baseDir, f)
		newPath := filepath.Join(defaultDir, f)

		if _, err := os.Stat(oldPath); err == nil {
			if err := os.Rename(oldPath, newPath); err != nil {
				return fmt.Errorf("move %s: %w", f, err)
			}
			fmt.Printf("  Moved: %s -> default/%s\n", f, f)
		}
	}

	// Move optional VM files (not IPSW - that goes to cache)
	optionalFiles := []string{"boot-args.txt"}
	for _, f := range optionalFiles {
		oldPath := filepath.Join(baseDir, f)
		newPath := filepath.Join(defaultDir, f)

		if _, err := os.Stat(oldPath); err == nil {
			if err := os.Rename(oldPath, newPath); err != nil {
				fmt.Printf("  warning: could not move %s: %v\n", f, err)
			} else {
				fmt.Printf("  Moved: %s -> default/%s\n", f, f)
			}
		}
	}

	// Move IPSW to shared cache directory
	cacheDir := GetCacheDir()
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

	// Set default as active VM
	if err := SetActiveVM("default"); err != nil {
		fmt.Printf("  warning: could not set active VM: %v\n", err)
	}

	fmt.Println("Migration complete. Active VM is now 'default'.")
	return nil
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
		return filepath.Join(GetVMBaseDir(), vmName)
	}

	// If vmDir is not the default, use it directly (backwards compatibility)
	if vmDir != "" && vmDir != defaultVMDir && !isSubdir(vmDir, defaultVMDir) {
		return vmDir
	}

	// Use active VM or default to "default"
	activeVM := GetActiveVM()
	return filepath.Join(GetVMBaseDir(), activeVM)
}

// isSubdir checks if path is a subdirectory of base.
func isSubdir(path, base string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return rel != ".." && !filepath.IsAbs(rel) && rel[0] != '.'
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

	return resolvePath(resolvedDir), nil
}

// detectOSType determines the OS type of a VM by checking for characteristic files.
// hw.model → macOS, efi.nvram/efi-vars.img → Linux, otherwise unknown.
func detectOSType(vmPath string) string {
	if _, err := os.Stat(filepath.Join(vmPath, "hw.model")); err == nil {
		return "macOS"
	}
	if _, err := os.Stat(filepath.Join(vmPath, "efi.nvram")); err == nil {
		return "Linux"
	}
	if _, err := os.Stat(filepath.Join(vmPath, "efi-vars.img")); err == nil {
		return "Linux"
	}
	return "unknown"
}
