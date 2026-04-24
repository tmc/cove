// networking.go - Advanced networking support (NAT, bridged, VMNet)
package main

import (
	"fmt"

	vz "github.com/tmc/apple/virtualization"
	networkx "github.com/tmc/apple/x/vzkit/network"
)

const (
	NetworkModeNAT                      = networkx.ModeNAT
	NetworkModeBridged                  = networkx.ModeBridged
	NetworkModeHostOnly                 = networkx.ModeHostOnly
	NetworkModeVMNet                    = networkx.ModeVMNet
	NetworkModeNone                     = networkx.ModeNone
	NetworkModeFileHandle networkx.Mode = "filehandle"
)

// ParseNetworkMode parses a network mode string.
func ParseNetworkMode(s string) (networkx.Config, error) {
	if s == string(NetworkModeFileHandle) {
		return networkx.Config{Mode: NetworkModeFileHandle}, nil
	}
	return networkx.Parse(s)
}

// CreateNetworkDeviceConfiguration creates a complete network device configuration.
func CreateNetworkDeviceConfiguration(config networkx.Config) (vz.VZVirtioNetworkDeviceConfiguration, error) {
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
  -network bridged:en1      Bridge to WiFi (check 'cove network list')
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
	fmt.Println("Usage: cove run -network bridged:<identifier>")
	fmt.Println("Example: cove run -network bridged:en0")
}
