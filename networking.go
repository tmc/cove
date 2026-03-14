// networking.go - Advanced networking support (NAT, bridged, VMNet)
package main

import (
	"fmt"

	vz "github.com/tmc/apple/virtualization"
	"github.com/tmc/apple/x/vzkit"
)

// NetworkMode represents the type of network configuration.
type NetworkMode = vzkit.NetworkMode

const (
	NetworkModeNAT     = vzkit.NetworkModeNAT
	NetworkModeBridged = vzkit.NetworkModeBridged
	NetworkModeVMNet   = vzkit.NetworkModeVMNet
	NetworkModeNone    = vzkit.NetworkModeNone
)

// NetworkConfig holds network configuration settings.
type NetworkConfig = vzkit.NetworkConfig

// ParseNetworkMode parses a network mode string.
func ParseNetworkMode(s string) (NetworkConfig, error) {
	return vzkit.ParseNetworkMode(s)
}

// CreateNetworkDeviceConfiguration creates a complete network device configuration.
func CreateNetworkDeviceConfiguration(config NetworkConfig) (vz.VZVirtioNetworkDeviceConfiguration, error) {
	return vzkit.CreateNetworkDevice(config)
}

// NetworkModeHelp returns help text for network modes.
func NetworkModeHelp() string {
	return `Network modes:
  nat              NAT networking (default, guest gets private IP via DHCP)
  bridged:<iface>  Bridge to host interface (e.g., bridged:en0)
                   Guest appears on same network as host
  vmnet            VMNet shared networking (macOS 14+)
  none             No networking

Examples:
  -network nat              Default NAT mode
  -network bridged:en0      Bridge to ethernet
  -network bridged:en1      Bridge to WiFi (check 'vz-macos network list')
  -network none             Disable networking`
}

// printNetworkInterfaces prints available network interfaces.
func printNetworkInterfaces() {
	fmt.Println("Available network interfaces for bridged mode:")
	fmt.Println()
	fmt.Println("To find your network interfaces, run:")
	fmt.Println("  networksetup -listallhardwareports")
	fmt.Println()
	fmt.Println("Common interface names:")
	fmt.Println("  en0        Built-in Ethernet or WiFi (primary)")
	fmt.Println("  en1        Secondary network interface")
	fmt.Println("  bridge0    Thunderbolt Bridge")
	fmt.Println()
	fmt.Println("Usage: vz-macos run -network bridged:<identifier>")
	fmt.Println("Example: vz-macos run -network bridged:en0")
}
