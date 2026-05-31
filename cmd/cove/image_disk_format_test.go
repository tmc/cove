package main

import (
	"errors"
	"testing"

	"github.com/tmc/cove/internal/diskimages2"
)

func TestDetectImageDiskFormat(t *testing.T) {
	old := retrieveDiskImageInfo
	t.Cleanup(func() { retrieveDiskImageInfo = old })

	retrieveDiskImageInfo = func(string) (*diskimages2.ImageInfo, error) {
		return &diskimages2.ImageInfo{Raw: map[string]string{"Image Format": "ASIF"}}, nil
	}
	if got := detectImageDiskFormat("/tmp/disk.img"); got != "asif" {
		t.Fatalf("detectImageDiskFormat(ASIF) = %q, want asif", got)
	}

	retrieveDiskImageInfo = func(string) (*diskimages2.ImageInfo, error) {
		return nil, errors.New("not a disk image")
	}
	if got := detectImageDiskFormat("/tmp/disk.img"); got != "raw" {
		t.Fatalf("detectImageDiskFormat(error) = %q, want raw", got)
	}
}

func TestNormalizeImageDiskFormat(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"RAW", "raw"},
		{"ASIF", "asif"},
		{"qcow2", "qcow2"},
	}
	for _, tc := range cases {
		if got := normalizeImageDiskFormat(tc.in); got != tc.want {
			t.Fatalf("normalizeImageDiskFormat(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
