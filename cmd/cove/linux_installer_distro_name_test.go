package main

import "testing"

func TestLinuxVariantDistroName(t *testing.T) {
	tests := []struct {
		v    LinuxVariant
		want string
	}{
		{LinuxVariantDesktop, "ubuntu"},
		{LinuxVariantServer, "ubuntu"},
		{LinuxVariantDebian, "debian"},
		{LinuxVariantFedora, "fedora"},
		{LinuxVariantAlpine, "alpine"},
		{LinuxVariantNixOS, "nixos"},
	}
	for _, tt := range tests {
		t.Run(string(tt.v), func(t *testing.T) {
			if got := tt.v.distroName(); got != tt.want {
				t.Fatalf("%s.distroName() = %q, want %q", tt.v, got, tt.want)
			}
		})
	}
}
