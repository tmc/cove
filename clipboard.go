// clipboard.go - Host↔guest clipboard sharing via SPICE agent.
//
// Delegates to github.com/tmc/apple/x/vzkit/clipboard for the implementation.
package main

import (
	"fmt"

	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/apple/x/vzkit/clipboard"
)

// createClipboardConfig creates a Virtio console device with SPICE agent
// clipboard sharing enabled. Returns a zero-value config if creation fails.
func createClipboardConfig() vz.VZVirtioConsoleDeviceConfiguration {
	cfg, err := clipboard.NewConfig()
	if err != nil {
		fmt.Printf("  warning: clipboard: %v\n", err)
		return vz.VZVirtioConsoleDeviceConfiguration{}
	}
	return cfg
}
