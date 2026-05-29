package main

import (
	"strings"
	"testing"

	"github.com/tmc/cove/internal/vmconfig"
)

func TestPrintVolumeMountInfoEmpty(t *testing.T) {
	out := captureStdout(t, func() error {
		printVolumeMountInfo(nil)
		return nil
	})
	if out != "" {
		t.Errorf("printVolumeMountInfo(nil) = %q, want empty", out)
	}
}

func TestPrintVolumeMountInfoReadOnlyAndOpts(t *testing.T) {
	oldLinux := linuxMode
	linuxMode = true
	defer func() { linuxMode = oldLinux }()

	out := captureStdout(t, func() error {
		printVolumeMountInfo([]vmconfig.VolumeMount{
			{HostPath: "/src", Tag: "src", ReadOnly: true, MountOpts: []string{"noatime", "ro"}},
		})
		return nil
	})
	for _, want := range []string{
		"/src -> tag \"src\" (ro [noatime,ro])",
		"guest: mount -t virtiofs",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestPrintVolumeMountInfoMultipleUntaggedRenamesDuplicates(t *testing.T) {
	oldLinux := linuxMode
	linuxMode = false
	defer func() { linuxMode = oldLinux }()

	out := captureStdout(t, func() error {
		printVolumeMountInfo([]vmconfig.VolumeMount{
			{HostPath: "/a/data"},
			{HostPath: "/b/data"},
			{HostPath: "/c/data"},
		})
		return nil
	})
	for _, want := range []string{
		"/a/data -> /Volumes/My Shared Files/data ",
		"/b/data -> /Volumes/My Shared Files/data-2 ",
		"/c/data -> /Volumes/My Shared Files/data-3 ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestPrintVolumeMountInfoMultipleUntaggedLinux(t *testing.T) {
	oldLinux := linuxMode
	linuxMode = true
	defer func() { linuxMode = oldLinux }()

	out := captureStdout(t, func() error {
		printVolumeMountInfo([]vmconfig.VolumeMount{
			{HostPath: "/a/data"},
			{HostPath: "/b/data"},
		})
		return nil
	})
	for _, want := range []string{
		"/a/data -> /mnt/data ",
		"/b/data -> /mnt/data-2 ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}
