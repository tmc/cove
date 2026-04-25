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
	want := []string{"mount", "-t", "virtiofs", "-o", "ro,cache=none,nodev,noatime", "work", "/mnt/work"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("virtioFSMountArgs(linux) = %#v, want %#v", got, want)
	}
}

// TestVirtioFSMountArgsLinuxDefaultsCacheNone verifies that with NO user
// opts, Linux mounts still get cache=none injected. This is the path the
// brief targets: `cove run -linux -vol ~/code:work` should give a
// cache-coherent guest mount without the user knowing about the option.
func TestVirtioFSMountArgsLinuxDefaultsCacheNone(t *testing.T) {
	m := vmconfig.VolumeMount{Tag: "work"}
	got := virtioFSMountArgs(m, "/mnt/work", true)
	want := []string{"mount", "-t", "virtiofs", "-o", "cache=none", "work", "/mnt/work"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("virtioFSMountArgs(linux default) = %#v, want %#v", got, want)
	}
}

// TestVirtioFSMountArgsLinuxRespectsExplicitCache verifies that a user who
// deliberately picks a different cache mode (e.g. cache=metadata for a
// read-mostly workload) keeps their setting; the helper does not silently
// double-inject cache=none.
func TestVirtioFSMountArgsLinuxRespectsExplicitCache(t *testing.T) {
	m := vmconfig.VolumeMount{
		Tag:       "work",
		MountOpts: []string{"cache=metadata", "noatime"},
	}
	got := virtioFSMountArgs(m, "/mnt/work", true)
	want := []string{"mount", "-t", "virtiofs", "-o", "cache=metadata,noatime", "work", "/mnt/work"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("virtioFSMountArgs(linux explicit cache) = %#v, want %#v", got, want)
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
