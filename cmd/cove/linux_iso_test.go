package main

import (
	"archive/tar"
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

func writeTarWithDiskInfo(t *testing.T, info string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake.iso")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	tw := tar.NewWriter(f)
	body := []byte(info)
	hdr := &tar.Header{Name: ".disk/info", Mode: 0o644, Size: int64(len(body))}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLinuxISOMatchesVariantClassifiesDiskInfo(t *testing.T) {
	ubuntuServer := writeTarWithDiskInfo(t, "Ubuntu-Server 24.04 LTS arm64")
	debian := writeTarWithDiskInfo(t, "Debian GNU/Linux 12 arm64")

	tests := []struct {
		name    string
		path    string
		variant LinuxVariant
		want    bool
	}{
		{"server matches ubuntu-server info", ubuntuServer, LinuxVariantServer, true},
		{"desktop rejects server info", ubuntuServer, LinuxVariantDesktop, false},
		{"debian matches debian info", debian, LinuxVariantDebian, true},
		{"alpine rejects debian info", debian, LinuxVariantAlpine, false},
		{"unknown variant returns false", ubuntuServer, LinuxVariant("bogus"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := linuxISOMatchesVariant(tc.path, tc.variant); got != tc.want {
				t.Fatalf("linuxISOMatchesVariant(%s) = %v, want %v", tc.variant, got, tc.want)
			}
		})
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

func TestEnsureLinuxISOForVariantIgnoresAria2Partial(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	saved := isoPath
	t.Cleanup(func() { isoPath = saved })
	isoPath = ""

	cacheDir := filepath.Join(home, ".vz", "cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		t.Fatal(err)
	}
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
	if err := os.WriteFile(cacheFile+".aria2", []byte("incomplete"), 0o644); err != nil {
		t.Fatal(err)
	}

	legacy := filepath.Join(cacheDir, "linux.iso")
	data, err := os.ReadFile(writeTarWithDiskInfo(t, "Alpine Linux v3.23 arm64"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(legacy, 31*1024*1024); err != nil {
		t.Fatal(err)
	}

	got, err := ensureLinuxISOForVariant(LinuxVariantAlpine)
	if err != nil {
		t.Fatalf("ensureLinuxISOForVariant: %v", err)
	}
	if got != legacy {
		t.Fatalf("got = %q, want legacy cache %q", got, legacy)
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
