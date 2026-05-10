package main

import (
	"errors"
	"strings"
	"testing"
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
