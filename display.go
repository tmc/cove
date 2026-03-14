// display.go - Multi-display support for VMs
//
// This file delegates to vzkit for core display configuration.
// App-specific helpers (verbose logging) remain here.
package main

import (
	"fmt"

	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/apple/x/vzkit"
)

// DisplayConfig is an alias for the vzkit display configuration type.
type DisplayConfig = vzkit.DisplayConfig

// DisplaySlice is an alias for the vzkit display slice type.
type DisplaySlice = vzkit.DisplaySlice

// DefaultDisplayConfig returns the default display configuration.
func DefaultDisplayConfig() DisplayConfig {
	return vzkit.DefaultDisplayConfig()
}

// CreateMacGraphicsConfig creates a macOS graphics device configuration
// with the specified displays (single or multiple).
func CreateMacGraphicsConfig(displays []DisplayConfig) (vz.VZMacGraphicsDeviceConfiguration, error) {
	graphicsConfig, err := vzkit.CreateMacGraphicsConfig(displays)
	if err != nil {
		return graphicsConfig, err
	}
	if verbose {
		for i, d := range displays {
			fmt.Printf("  Display %d: %dx%d @ %d PPI\n", i+1, d.Width, d.Height, d.PPI)
		}
	}
	return graphicsConfig, nil
}

// CreateVirtioGraphicsConfig creates a Linux/generic graphics device configuration
// with the specified displays (for VirtIO GPU).
func CreateVirtioGraphicsConfig(displays []DisplayConfig) (vz.VZVirtioGraphicsDeviceConfiguration, error) {
	return vzkit.CreateVirtioGraphicsConfig(displays)
}

// GetDefaultDisplayForVM returns the appropriate default display for a VM type.
func GetDefaultDisplayForVM(isLinux bool) DisplayConfig {
	if isLinux {
		return vzkit.DefaultLinuxDisplayConfig()
	}
	return vzkit.DefaultDisplayConfig()
}
