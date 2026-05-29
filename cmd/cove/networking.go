// networking.go - Advanced networking support (NAT, bridged, VMNet)

package main

import (
	"errors"
	"fmt"
	"strings"

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

var (
	ErrInvalidNetworkSpec         = errors.New("invalid network spec")
	lookupBridgedNetworkInterface = networkx.LookupBridgedNetworkInterface
)

// ParseNetworkMode parses a network mode string.
func ParseNetworkMode(s string) (networkx.Config, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if policy, ok := parseNamedNetworkPolicyMode(s); ok {
		return policy.NetworkConfig(), nil
	}
	if s == string(NetworkModeFileHandle) {
		return networkx.Config{Mode: NetworkModeFileHandle}, nil
	}
	cfg, err := networkx.Parse(s)
	if err != nil {
		return networkx.Config{}, fmt.Errorf("%w: %v", ErrInvalidNetworkSpec, err)
	}
	if cfg.Mode == NetworkModeBridged {
		if _, err := lookupBridgedNetworkInterface(cfg.Interface); err != nil {
			return networkx.Config{}, fmt.Errorf("%w: %v", ErrInvalidNetworkSpec, err)
		}
	}
	return cfg, nil
}

func validateNetworkMode(s string) error {
	_, err := ParseNetworkPolicy(s)
	return err
}

func parseNamedNetworkPolicyMode(s string) (NetworkPolicy, bool) {
	switch s {
	case "offline", "packages", "host-services", "lan", "open":
		p, err := ParseNetworkPolicy(s)
		return p, err == nil
	default:
		return NetworkPolicy{}, false
	}
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
	return `Usage: cove network [list|audit|logs] [args]

Network modes:
  nat              NAT networking (default, guest gets private IP via DHCP)
  bridged:<iface>  Bridge to host interface (e.g., bridged:en0)
                   Guest appears on same network as host
  host-only        Private host-only network between host and guest
  vmnet            VMNet shared networking (macOS 14+)
  filehandle       Host-side filehandle attachment for raw frame capture
  none             No networking

Named policies:
  offline          No network device
  packages         NAT with package-registry allowlist audit
  host-services    NAT with package-registry plus RFC1918 audit
  lan              NAT with RFC1918-only audit intent
  open             Full egress, equivalent to nat

Commands:
  cove network list            List host interfaces for bridged mode
  cove network audit <run-id>  Print a run's network.log
  cove network logs <vm> [-f]  Print or follow the newest audit log for a VM

Examples:
  --net nat                 Default NAT mode
  --net packages            Package-registry policy audit
  --net bridged:en0         Bridge to ethernet
  --net host-only           Host and guest only
  --net bridged:en1         Bridge to WiFi (check 'cove network list')
  --net filehandle          Raw frame capture via VZFileHandleNetworkDeviceAttachment
  --net none                Disable networking`
}

// printNetworkInterfaces prints available network interfaces.
func printNetworkInterfaces() {
	fmt.Println("Network interface guidance for bridged mode:")
	fmt.Println()
	fmt.Println("This command does not enumerate interfaces yet. To find your network interfaces, run:")
	fmt.Println("  networksetup -listallhardwareports")
	fmt.Println()
	fmt.Println("Common interface names:")
	fmt.Println("  en0        Built-in Ethernet or WiFi (primary)")
	fmt.Println("  en1        Secondary network interface")
	fmt.Println("  bridge0    Thunderbolt Bridge")
	fmt.Println()
	fmt.Println("Usage: cove run --net bridged:<identifier>")
	fmt.Println("Example: cove run --net bridged:en0")
}
