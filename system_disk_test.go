package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	storagex "github.com/tmc/apple/x/vzkit/storage"
)

func TestSystemDiskAttachmentModeString(t *testing.T) {
	tests := []struct {
		name string
		mode systemDiskAttachmentMode
		want string
	}{
		{name: "disk image (zero value)", mode: systemDiskAttachmentDiskImage, want: "disk-image"},
		{name: "temporary ram", mode: systemDiskAttachmentTemporaryRAM, want: "temporary-ram"},
		{name: "out of range falls through to default", mode: systemDiskAttachmentMode(99), want: "disk-image"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.mode.String(); got != tt.want {
				t.Fatalf("(%d).String() = %q, want %q", tt.mode, got, tt.want)
			}
		})
	}
}

func TestEffectiveSystemDiskPath(t *testing.T) {
	prev := runtimeSystemDiskPathOverride
	t.Cleanup(func() { runtimeSystemDiskPathOverride = prev })

	runtimeSystemDiskPathOverride = ""
	if got := effectiveSystemDiskPath("/orig/disk.img"); got != "/orig/disk.img" {
		t.Fatalf("no override: got %q", got)
	}
	runtimeSystemDiskPathOverride = "   "
	if got := effectiveSystemDiskPath("/orig/disk.img"); got != "/orig/disk.img" {
		t.Fatalf("whitespace override should be ignored: got %q", got)
	}
	runtimeSystemDiskPathOverride = "/override/disk.img"
	if got := effectiveSystemDiskPath("/orig/disk.img"); got != "/override/disk.img" {
		t.Fatalf("override active: got %q", got)
	}
}

func TestVMPrimaryDiskPath(t *testing.T) {
	tests := []struct {
		name   string
		marker string
		want   string
	}{
		{name: "macos default", marker: "hw.model", want: "disk.img"},
		{name: "linux", marker: "efi.nvram", want: "linux-disk.img"},
		{name: "windows", marker: "windows-disk.img", want: "windows-disk.img"},
		{name: "unknown", marker: "", want: "disk.img"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.marker != "" {
				if err := os.WriteFile(filepath.Join(dir, tt.marker), nil, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got := vmPrimaryDiskPath(dir)
			want := filepath.Join(dir, tt.want)
			if got != want {
				t.Fatalf("vmPrimaryDiskPath(%s) = %q, want %q", tt.marker, got, want)
			}
		})
	}
}

func TestDiskImageSyncMode(t *testing.T) {
	old := diskSyncMode
	t.Cleanup(func() { diskSyncMode = old })

	tests := []struct {
		name string
		flag string
	}{
		{"default", ""},
		{"fsync", "fsync"},
		{"none", "none"},
		{"full", "full"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diskSyncMode = tt.flag
			if _, err := diskImageSyncMode(storagex.CacheDurable); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDiskImageSyncModeInvalid(t *testing.T) {
	old := diskSyncMode
	t.Cleanup(func() { diskSyncMode = old })

	diskSyncMode = "frobnicate"
	_, err := diskImageSyncMode(storagex.CacheDurable)
	if err == nil || !strings.Contains(err.Error(), "invalid -disk-sync") {
		t.Fatalf("err = %v, want invalid -disk-sync", err)
	}
}

func TestSelectedVMSourceName(t *testing.T) {
	prevName, prevDir := vmName, vmDir
	t.Cleanup(func() { vmName, vmDir = prevName, prevDir })

	vmName = "explicit-vm"
	vmDir = "/some/path/other-vm"
	if got := selectedVMSourceName(); got != "explicit-vm" {
		t.Fatalf("explicit name: got %q", got)
	}
	vmName = "  trim-me  "
	if got := selectedVMSourceName(); got != "trim-me" {
		t.Fatalf("trimmed name: got %q", got)
	}
	vmName = ""
	vmDir = "/path/to/from-dir"
	if got := selectedVMSourceName(); got != "from-dir" {
		t.Fatalf("fallback to vmDir basename: got %q", got)
	}
	vmName = "   "
	if got := selectedVMSourceName(); got != "from-dir" {
		t.Fatalf("blank name falls back to vmDir basename: got %q", got)
	}
}
