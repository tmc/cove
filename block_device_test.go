package main

import (
	"testing"

	vz "github.com/tmc/apple/virtualization"
)

func TestParseBlockDeviceSpec(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    blockDeviceSpec
		wantErr bool
	}{
		{
			name: "read only",
			in:   "/dev/rdisk8:ro",
			want: blockDeviceSpec{Path: "/dev/rdisk8", ReadOnly: true},
		},
		{
			name: "read write",
			in:   "/dev/rdisk8:rw",
			want: blockDeviceSpec{Path: "/dev/rdisk8"},
		},
		{
			name: "sync full",
			in:   "/dev/rdisk8:rw:sync=full",
			want: blockDeviceSpec{Path: "/dev/rdisk8", Sync: "full"},
		},
		{
			name: "sync none",
			in:   "/dev/rdisk8:rw:sync=none",
			want: blockDeviceSpec{Path: "/dev/rdisk8", Sync: "none"},
		},
		{name: "empty", in: "", wantErr: true},
		{name: "relative", in: "rdisk8:ro", wantErr: true},
		{name: "missing mode", in: "/dev/rdisk8", wantErr: true},
		{name: "bad mode", in: "/dev/rdisk8:write", wantErr: true},
		{name: "bad option", in: "/dev/rdisk8:rw:cache=none", wantErr: true},
		{name: "sync none read only", in: "/dev/rdisk8:ro:sync=none", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseBlockDeviceSpec(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseBlockDeviceSpec(%q) error = nil", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseBlockDeviceSpec(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("parseBlockDeviceSpec(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

func TestBlockDeviceSyncMode(t *testing.T) {
	tests := []struct {
		name    string
		spec    blockDeviceSpec
		want    vz.VZDiskSynchronizationMode
		wantErr bool
	}{
		{"ro", blockDeviceSpec{Path: "/dev/rdisk8", ReadOnly: true}, vz.VZDiskSynchronizationModeFull, false},
		{"rw", blockDeviceSpec{Path: "/dev/rdisk8"}, vz.VZDiskSynchronizationModeFull, false},
		{"rw full", blockDeviceSpec{Path: "/dev/rdisk8", Sync: "full"}, vz.VZDiskSynchronizationModeFull, false},
		{"rw none", blockDeviceSpec{Path: "/dev/rdisk8", Sync: "none"}, vz.VZDiskSynchronizationModeNone, false},
		{"ro none", blockDeviceSpec{Path: "/dev/rdisk8", ReadOnly: true, Sync: "none"}, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := blockDeviceSyncMode(tt.spec)
			if tt.wantErr {
				if err == nil {
					t.Fatal("blockDeviceSyncMode error = nil")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("blockDeviceSyncMode = %v, want %v", got, tt.want)
			}
		})
	}
}
