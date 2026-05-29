package main

import (
	"errors"
	"os"
	"strings"
	"testing"

	vz "github.com/tmc/apple/virtualization"
)

func TestBlockDeviceSliceString(t *testing.T) {
	var nilSlice *blockDeviceSlice
	if got := nilSlice.String(); got != "" {
		t.Fatalf("nil String = %q, want empty", got)
	}
	empty := blockDeviceSlice{}
	if got := empty.String(); got != "" {
		t.Fatalf("empty String = %q, want empty", got)
	}
	s := blockDeviceSlice{
		{Path: "/dev/rdisk8", ReadOnly: true},
		{Path: "/dev/rdisk9"},
		{Path: "/dev/rdisk10", Sync: "none"},
	}
	want := "/dev/rdisk8:ro, /dev/rdisk9:rw, /dev/rdisk10:rw:sync=none"
	if got := s.String(); got != want {
		t.Fatalf("String = %q, want %q", got, want)
	}
}

func TestBlockDeviceSliceSet(t *testing.T) {
	var s blockDeviceSlice
	if err := s.Set("/dev/rdisk8:ro"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Set("/dev/rdisk9:rw:sync=full"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if len(s) != 2 {
		t.Fatalf("len = %d, want 2", len(s))
	}
	if s[0].Path != "/dev/rdisk8" || !s[0].ReadOnly {
		t.Fatalf("s[0] = %+v", s[0])
	}
	if s[1].Sync != "full" {
		t.Fatalf("s[1] = %+v", s[1])
	}
	if err := s.Set("bogus"); err == nil {
		t.Fatal("Set(bogus) err = nil, want error")
	}
}

func TestBlockDeviceOpenFlags(t *testing.T) {
	if got := blockDeviceOpenFlags(true); got != os.O_RDONLY {
		t.Fatalf("readOnly flags = %d, want %d", got, os.O_RDONLY)
	}
	if got := blockDeviceOpenFlags(false); got != os.O_RDWR {
		t.Fatalf("readWrite flags = %d, want %d", got, os.O_RDWR)
	}
}

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

func TestOpenBlockDeviceViaHelperFailureText(t *testing.T) {
	tests := []struct {
		name      string
		installed bool
		fresh     bool
		freshErr  error
		want      string
	}{
		{
			name:      "not installed",
			installed: false,
			want:      "block devices require an up-to-date cove-helper; run: sudo cove helper install",
		},
		{
			name:      "stale helper",
			installed: true,
			fresh:     false,
			want:      "block devices require an up-to-date cove-helper; run: sudo cove helper install",
		},
		{
			name:      "freshness check error",
			installed: true,
			freshErr:  errors.New("hash running binary: boom"),
			want:      "check cove-helper freshness: hash running binary: boom",
		},
	}

	oldInstalled := helperInstalled
	oldFresh := helperBinaryFreshness
	t.Cleanup(func() {
		helperInstalled = oldInstalled
		helperBinaryFreshness = oldFresh
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			helperInstalled = func() bool { return tt.installed }
			helperBinaryFreshness = func() (bool, string, error) {
				return tt.fresh, "", tt.freshErr
			}
			_, err := openBlockDeviceViaHelper(blockDeviceSpec{Path: "/dev/rdisk8"})
			if err == nil {
				t.Fatal("openBlockDeviceViaHelper err = nil, want error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("openBlockDeviceViaHelper err = %q, want substring %q", err, tt.want)
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
