package main

import (
	"net/netip"
	"testing"
)

func TestParseNetworkModeExplicitModes(t *testing.T) {
	tests := []struct {
		name string
		in   string
		mode string
	}{
		{name: "empty default", in: "", mode: string(NetworkModeNAT)},
		{name: "nat", in: "nat", mode: string(NetworkModeNAT)},
		{name: "net spelling", in: "HOST-ONLY", mode: string(NetworkModeHostOnly)},
		{name: "none", in: "none", mode: string(NetworkModeNone)},
		{name: "filehandle", in: "filehandle", mode: string(NetworkModeFileHandle)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseNetworkMode(tt.in)
			if err != nil {
				t.Fatalf("ParseNetworkMode(%q): %v", tt.in, err)
			}
			if string(got.Mode) != tt.mode {
				t.Fatalf("mode = %q, want %q", got.Mode, tt.mode)
			}
		})
	}
}

func TestParseNetworkModeRejectsBareBridged(t *testing.T) {
	if _, err := ParseNetworkMode("bridged"); err == nil {
		t.Fatal("ParseNetworkMode(bridged) succeeded, want error")
	}
}

func TestParseNetworkModeNamedPolicies(t *testing.T) {
	tests := []struct {
		name string
		in   string
		mode string
	}{
		{name: "offline", in: "offline", mode: string(NetworkModeNone)},
		{name: "packages", in: "packages", mode: string(NetworkModeNAT)},
		{name: "host services", in: "host-services", mode: string(NetworkModeNAT)},
		{name: "lan", in: "lan", mode: string(NetworkModeNAT)},
		{name: "open", in: "open", mode: string(NetworkModeNAT)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseNetworkMode(tt.in)
			if err != nil {
				t.Fatalf("ParseNetworkMode(%q): %v", tt.in, err)
			}
			if string(got.Mode) != tt.mode {
				t.Fatalf("mode = %q, want %q", got.Mode, tt.mode)
			}
		})
	}
}

func TestParseNetworkPolicyBehavior(t *testing.T) {
	tests := []struct {
		name         string
		in           string
		wantAudit    bool
		wantDomain   string
		wantIP       string
		wantDomainOK bool
		wantIPOK     bool
	}{
		{name: "offline", in: "offline", wantAudit: true, wantDomain: "pypi.org", wantIP: "192.168.1.5"},
		{name: "packages", in: "packages", wantAudit: true, wantDomain: "files.pythonhosted.org", wantDomainOK: true, wantIP: "192.168.1.5"},
		{name: "host services", in: "host-services", wantAudit: true, wantDomain: "registry.npmjs.org", wantDomainOK: true, wantIP: "10.1.2.3", wantIPOK: true},
		{name: "lan", in: "lan", wantAudit: true, wantDomain: "pypi.org", wantIP: "172.16.9.8", wantIPOK: true},
		{name: "open", in: "open", wantDomain: "example.com", wantDomainOK: true, wantIP: "8.8.8.8", wantIPOK: true},
		{name: "nat", in: "nat", wantDomain: "example.com", wantIP: "8.8.8.8"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseNetworkPolicy(tt.in)
			if err != nil {
				t.Fatalf("ParseNetworkPolicy(%q): %v", tt.in, err)
			}
			if got.ShouldAudit() != tt.wantAudit {
				t.Fatalf("ShouldAudit = %v, want %v", got.ShouldAudit(), tt.wantAudit)
			}
			if got.AllowsDomain(tt.wantDomain) != tt.wantDomainOK {
				t.Fatalf("AllowsDomain(%q) = %v, want %v", tt.wantDomain, got.AllowsDomain(tt.wantDomain), tt.wantDomainOK)
			}
			addr := netip.MustParseAddr(tt.wantIP)
			if got.AllowsIP(addr) != tt.wantIPOK {
				t.Fatalf("AllowsIP(%q) = %v, want %v", tt.wantIP, got.AllowsIP(addr), tt.wantIPOK)
			}
		})
	}
}
