// vm_registry.go - VM discovery and management
package main

import (
	"flag"
	"fmt"
	"net"
	"time"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

// applyVMConfig loads saved VM config and applies defaults for flags
// not explicitly set by the user. When the user does pass -cpu or -memory,
// the new value is saved to config.json for subsequent boots.
func applyVMConfig(dir string) {
	cfg, err := vmconfig.Load(dir)
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

	hardware, changed := vmconfig.ApplyHardware(cfg,
		vmconfig.Hardware{CPU: cpuCount, MemoryGB: memoryGB},
		vmconfig.HardwareExplicit{CPU: cpuSet, MemoryGB: memSet},
	)
	cpuCount = hardware.CPU
	memoryGB = hardware.MemoryGB

	if changed {
		if err := vmconfig.Save(dir, cfg); err != nil {
			fmt.Printf("warning: save vm config: %v\n", err)
		}
	}
}

// savePostInstallRecipes persists the selected post-install recipes
// so they can be retried if installation or scripting fails.
func savePostInstallRecipes(dir, recipes string) {
	if err := vmconfig.SetPostInstallRecipes(dir, recipes); err != nil {
		fmt.Printf("warning: save vzscript config: %v\n", err)
	}
}

// saveHardwareConfig persists the current CPU and memory settings.
func saveHardwareConfig(dir string) {
	changed, err := vmconfig.SetHardware(dir, vmconfig.Hardware{CPU: cpuCount, MemoryGB: memoryGB})
	if err != nil && changed {
		fmt.Printf("warning: save vm config: %v\n", err)
	}
}

// GetVMInfo returns information about a specific VM.
func GetVMInfo(vmPath string) (*vmconfig.Info, error) {
	return vmconfig.InfoFor(vmPath, detectVMState)
}

func detectVMState(vmPath string) string {
	if isVMRunningAt(vmPath) {
		return "running"
	}
	if vmconfig.HasSuspendState(vmPath) {
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
func ListVMs() ([]vmconfig.Info, error) {
	return vmconfig.List(detectVMState)
}

// ResolveVMDir returns the VM directory to use.
// If a specific VM name is given (via -vm flag), use that.
// Otherwise, use the vmDir flag value or the active VM.
func ResolveVMDir(vmName string) string {
	return vmconfig.ResolveDir(vmName, vmDir)
}

// EnsureVMDir ensures the VM directory exists and runs migration if needed.
func EnsureVMDir(vmName string) (string, error) {
	return vmconfig.EnsureDir(vmName, vmDir)
}
