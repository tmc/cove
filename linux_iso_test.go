package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsURL(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"http://example.com/x.iso", true},
		{"https://example.com/x.iso", true},
		{"/local/path.iso", false},
		{"file:///x", false},
		{"", false},
		{"http://", false}, // len <= 8
		{"https://x", true},
		{"ftp://example.com/x.iso", false},
	}
	for _, tc := range cases {
		if got := isURL(tc.in); got != tc.want {
			t.Errorf("isURL(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestLinuxISOMatchesVariantMissingFile(t *testing.T) {
	// bsdtar against a missing file fails, function returns false for any variant.
	missing := filepath.Join(t.TempDir(), "nope.iso")
	for _, v := range []LinuxVariant{
		LinuxVariantServer, LinuxVariantDesktop, LinuxVariantDebian,
		LinuxVariantFedora, LinuxVariantAlpine, LinuxVariantNixOS,
		LinuxVariant("bogus"),
	} {
		if linuxISOMatchesVariant(missing, v) {
			t.Errorf("linuxISOMatchesVariant(missing, %s) = true, want false", v)
		}
	}
}

func TestEnsureLinuxISOForVariantUsesPrimaryCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	saved := isoPath
	t.Cleanup(func() { isoPath = saved })
	isoPath = ""

	cacheDir := filepath.Join(home, ".vz", "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Alpine minSize is 30 MiB; write a sparse 31 MiB file.
	cacheFile := filepath.Join(cacheDir, "linux-alpine.iso")
	f, err := os.Create(cacheFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(31 * 1024 * 1024); err != nil {
		f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := ensureLinuxISOForVariant(LinuxVariantAlpine)
	if err != nil {
		t.Fatalf("ensureLinuxISOForVariant: %v", err)
	}
	if got != cacheFile {
		t.Fatalf("got = %q, want %q", got, cacheFile)
	}
}

func TestEnsureLinuxISOForVariantUnsupported(t *testing.T) {
	if _, err := ensureLinuxISOForVariant(LinuxVariant("nope-distro")); err == nil {
		t.Fatal("ensureLinuxISOForVariant(nope-distro) err = nil, want error")
	}
}

func TestEnsureLinuxISOForVariantMissingIsoPath(t *testing.T) {
	saved := isoPath
	t.Cleanup(func() { isoPath = saved })
	isoPath = filepath.Join(t.TempDir(), "does-not-exist.iso")
	_, err := ensureLinuxISOForVariant(LinuxVariantServer)
	if err == nil {
		t.Fatal("ensureLinuxISOForVariant with missing isoPath = nil, want error")
	}
	if !strings.Contains(err.Error(), "iso file not found") {
		t.Errorf("err = %v, want 'iso file not found'", err)
	}
}
