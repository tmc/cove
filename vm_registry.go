// vm_registry.go - VM discovery and management
package main

import (
	"flag"
	"fmt"
	"net"
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

// GetVMInfo returns information about a specific VM.
func GetVMInfo(vmPath string) (*VMInfo, error) {
	return vmconfigInfoFor(vmPath, detectVMState)
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
	return vmconfigList(detectVMState)
}

// ResolveVMDir returns the VM directory to use.
// If a specific VM name is given (via -vm flag), use that.
// Otherwise, use the vmDir flag value or the active VM.
func ResolveVMDir(vmName string) string {
	return vmconfigResolveDir(vmName, vmDir)
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
	return vmconfigEnsureDir(vmName, vmDir)
}
