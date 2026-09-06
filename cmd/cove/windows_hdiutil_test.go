package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseHdiutilAttachOutputHappy(t *testing.T) {
	out := strings.Join([]string{
		"/dev/disk7          \tGUID_partition_scheme        \t",
		"/dev/disk7s1        \tEFI                          \t/Volumes/EFI",
		"/dev/disk7s2        \tWindows_FAT_32               \t/Volumes/MYUSB",
	}, "\n") + "\n"
	dev, mount, err := parseHdiutilAttachOutput(out)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if dev != "/dev/disk7" {
		t.Errorf("device = %q, want /dev/disk7", dev)
	}
	if mount != "/Volumes/MYUSB" {
		t.Errorf("mount = %q, want /Volumes/MYUSB", mount)
	}
}

func TestParseHdiutilAttachOutputNoDevice(t *testing.T) {
	_, _, err := parseHdiutilAttachOutput("garbage line\n")
	if !errors.Is(err, ErrHdiutilNoDevice) {
		t.Fatalf("err = %v, want ErrHdiutilNoDevice", err)
	}
}

func TestParseHdiutilAttachOutputNoMount(t *testing.T) {
	out := "/dev/disk9          \tGUID_partition_scheme        \t\n" +
		"/dev/disk9s1        \tWindows_FAT_32               \t\n"
	_, _, err := parseHdiutilAttachOutput(out)
	if !errors.Is(err, ErrHdiutilNoMountPoint) {
		t.Fatalf("err = %v, want ErrHdiutilNoMountPoint", err)
	}
}

func TestParseHdiutilAttachOutputIgnoresNonVolumesMount(t *testing.T) {
	out := "/dev/disk5          \tGUID_partition_scheme        \t\n" +
		"/dev/disk5s2        \tWindows_FAT_32               \t/private/somewhere\n"
	_, _, err := parseHdiutilAttachOutput(out)
	if !errors.Is(err, ErrHdiutilNoMountPoint) {
		t.Fatalf("err = %v, want ErrHdiutilNoMountPoint for non-Volumes mount", err)
	}
}

func TestInstallWindowsGOPShim(t *testing.T) {
	dir := t.TempDir()
	bootDir := filepath.Join(dir, "EFI", "Boot")
	msBootDir := filepath.Join(dir, "EFI", "Microsoft", "Boot")
	if err := os.MkdirAll(bootDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(msBootDir, 0755); err != nil {
		t.Fatal(err)
	}
	bootPath := filepath.Join(bootDir, "BOOTAA64.EFI")
	msBootPath := filepath.Join(msBootDir, "bootmgfw.efi")
	if err := os.WriteFile(bootPath, []byte("windows boot fallback"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(msBootPath, []byte("windows boot manager"), 0644); err != nil {
		t.Fatal(err)
	}
	shimPath := filepath.Join(dir, "gopshim-chain.efi")
	shim := []byte("gop shim")
	if err := os.WriteFile(shimPath, shim, 0644); err != nil {
		t.Fatal(err)
	}

	if err := installWindowsGOPShim(dir, shimPath); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(bootPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(shim) {
		t.Fatalf("BOOTAA64.EFI = %q, want shim", got)
	}
	got, err = os.ReadFile(msBootPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "windows boot manager" {
		t.Fatalf("Microsoft bootmgfw changed to %q", got)
	}
	if _, err := os.Stat(filepath.Join(bootDir, "bootmgfw.efi")); !os.IsNotExist(err) {
		t.Fatalf("EFI/Boot/bootmgfw.efi exists, want no synthesized boot manager copy")
	}
}

func TestInstallWindowsGOPShimFailsClosed(t *testing.T) {
	dir := t.TempDir()
	bootDir := filepath.Join(dir, "EFI", "Boot")
	msBootDir := filepath.Join(dir, "EFI", "Microsoft", "Boot")
	if err := os.MkdirAll(bootDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(msBootDir, 0755); err != nil {
		t.Fatal(err)
	}
	bootPath := filepath.Join(bootDir, "BOOTAA64.EFI")
	msBootPath := filepath.Join(msBootDir, "bootmgfw.efi")
	if err := os.WriteFile(bootPath, []byte("windows boot fallback"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(msBootPath, []byte("windows boot manager"), 0644); err != nil {
		t.Fatal(err)
	}

	err := installWindowsGOPShim(dir, filepath.Join(dir, "missing.efi"))
	if err == nil {
		t.Fatal("err = nil, want missing shim error")
	}
	got, err := os.ReadFile(bootPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "windows boot fallback" {
		t.Fatalf("BOOTAA64.EFI = %q, want original fallback", got)
	}
}

func TestWindowsEFIBootImageFreshRequiresShimCacheKey(t *testing.T) {
	dir := t.TempDir()
	bootImg := filepath.Join(dir, "efi-boot.img")
	cacheKey := bootImg + ".cachekey"
	iso := filepath.Join(dir, "windows.iso")
	shim := filepath.Join(dir, "gopshim.efi")

	if err := os.WriteFile(iso, []byte("iso"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shim, []byte("shim"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bootImg, []byte("boot"), 0644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	mid := time.Now().Add(-1 * time.Hour)
	newer := time.Now()
	if err := os.Chtimes(iso, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(shim, mid, mid); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(bootImg, newer, newer); err != nil {
		t.Fatal(err)
	}

	fresh, err := windowsEFIBootImageFresh(bootImg, cacheKey, iso, shim)
	if err != nil {
		t.Fatal(err)
	}
	if fresh {
		t.Fatal("fresh = true without shim cache key, want false")
	}

	wantKey, err := windowsEFIBootCacheKey(shim)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cacheKey, []byte(wantKey), 0644); err != nil {
		t.Fatal(err)
	}
	fresh, err = windowsEFIBootImageFresh(bootImg, cacheKey, iso, shim)
	if err != nil {
		t.Fatal(err)
	}
	if !fresh {
		t.Fatal("fresh = false with matching shim cache key, want true")
	}
}
