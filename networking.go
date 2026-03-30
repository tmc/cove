// networking.go - Advanced networking support (NAT, bridged, VMNet)
package main

import (
	"fmt"

	vz "github.com/tmc/apple/virtualization"
	networkx "github.com/tmc/apple/x/vzkit/network"
)

// NetworkMode represents the type of network configuration.
type NetworkMode = networkx.Mode

const (
	NetworkModeNAT                    = networkx.ModeNAT
	NetworkModeBridged                = networkx.ModeBridged
	NetworkModeHostOnly               = networkx.ModeHostOnly
	NetworkModeVMNet                  = networkx.ModeVMNet
	NetworkModeNone                   = networkx.ModeNone
	NetworkModeFileHandle NetworkMode = "filehandle"
)

// NetworkConfig holds network configuration settings.
type NetworkConfig = networkx.Config

// ParseNetworkMode parses a network mode string.
func ParseNetworkMode(s string) (NetworkConfig, error) {
	if s == string(NetworkModeFileHandle) {
		return NetworkConfig{Mode: NetworkModeFileHandle}, nil
	}
	return networkx.Parse(s)
}

// CreateNetworkDeviceConfiguration creates a complete network device configuration.
func CreateNetworkDeviceConfiguration(config NetworkConfig) (vz.VZVirtioNetworkDeviceConfiguration, error) {
	if config.Mode == NetworkModeFileHandle {
		return prepareFileHandleNetworkDevice()
	}
	return networkx.CreateDevice(config)
}

// NetworkModeHelp returns help text for network modes.
func NetworkModeHelp() string {
	return `Network modes:
  nat              NAT networking (default, guest gets private IP via DHCP)
  bridged:<iface>  Bridge to host interface (e.g., bridged:en0)
                   Guest appears on same network as host
  host-only        Private host-only network between host and guest
  vmnet            VMNet shared networking (macOS 14+)
  filehandle       Host-side filehandle attachment for raw frame capture
  none             No networking

Examples:
  -network nat              Default NAT mode
  -network bridged:en0      Bridge to ethernet
  -network host-only        Host and guest only
  -network bridged:en1      Bridge to WiFi (check 'vz-macos network list')
  -network filehandle       Raw frame capture via VZFileHandleNetworkDeviceAttachment
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
