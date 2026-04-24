package main

import (
	"reflect"
	"testing"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

func TestVirtioFSMountArgsLinux(t *testing.T) {
	m := vmconfig.VolumeMount{
		Tag:       "work",
		ReadOnly:  true,
		MountOpts: []string{"nodev", "noatime"},
	}
	got := virtioFSMountArgs(m, "/mnt/work", true)
	want := []string{"mount", "-t", "virtiofs", "-o", "ro,nodev,noatime", "work", "/mnt/work"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("virtioFSMountArgs(linux) = %#v, want %#v", got, want)
	}
}

func TestVirtioFSMountArgsMacOS(t *testing.T) {
	m := vmconfig.VolumeMount{
		Tag:       "work",
		ReadOnly:  true,
		MountOpts: []string{"noatime", "cache=none"},
	}
	got := virtioFSMountArgs(m, "/Volumes/work", false)
	want := []string{"mount_virtiofs", "-r", "work", "/Volumes/work"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("virtioFSMountArgs(macOS) = %#v, want %#v", got, want)
	}
}

func TestVirtioFSMountArgsMacOSReadWrite(t *testing.T) {
	m := vmconfig.VolumeMount{Tag: "data"}
	got := virtioFSMountArgs(m, "/Volumes/data", false)
	want := []string{"mount_virtiofs", "data", "/Volumes/data"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("virtioFSMountArgs(macOS rw) = %#v, want %#v", got, want)
	}
}
