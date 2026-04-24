// display.go - Multi-display support for VMs
//
// This file delegates to vzkit for core display configuration.
// App-specific helpers (verbose logging) remain here.
package main

import (
	"fmt"

	vz "github.com/tmc/apple/virtualization"
	displayx "github.com/tmc/apple/x/vzkit/display"
)

// DefaultDisplayConfig returns the default display configuration.
func DefaultDisplayConfig() displayx.Config {
	return displayx.DefaultConfig()
}

// CreateMacGraphicsConfig creates a macOS graphics device configuration
// with the specified displays (single or multiple).
func CreateMacGraphicsConfig(displays []displayx.Config) (vz.VZMacGraphicsDeviceConfiguration, error) {
	graphicsConfig, err := displayx.CreateMacGraphicsConfig(displays)
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
func CreateVirtioGraphicsConfig(displays []displayx.Config) (vz.VZVirtioGraphicsDeviceConfiguration, error) {
	return displayx.CreateVirtioGraphicsConfig(displays)
}

// GetDefaultDisplayForVM returns the appropriate default display for a VM type.
func GetDefaultDisplayForVM(isLinux bool) displayx.Config {
	if isLinux {
		return displayx.DefaultLinuxConfig()
	}
	return displayx.DefaultConfig()
}
