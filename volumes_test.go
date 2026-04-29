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
	want := []string{"mount", "-t", "virtiofs", "-o", "ro,cache=none,nodev,noatime,uid=1000,gid=1000", "work", "/mnt/work"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("virtioFSMountArgs(linux) = %#v, want %#v", got, want)
	}
}

func TestVirtioFSMountArgsLinuxDefaultsCacheNone(t *testing.T) {
	m := vmconfig.VolumeMount{Tag: "work"}
	got := virtioFSMountArgs(m, "/mnt/work", true)
	want := []string{"mount", "-t", "virtiofs", "-o", "cache=none,uid=1000,gid=1000", "work", "/mnt/work"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("virtioFSMountArgs(linux default) = %#v, want %#v", got, want)
	}
}

func TestVirtioFSMountArgsLinuxRespectsExplicitCache(t *testing.T) {
	m := vmconfig.VolumeMount{
		Tag:       "work",
		MountOpts: []string{"cache=metadata", "noatime"},
	}
	got := virtioFSMountArgs(m, "/mnt/work", true)
	want := []string{"mount", "-t", "virtiofs", "-o", "cache=metadata,noatime,uid=1000,gid=1000", "work", "/mnt/work"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("virtioFSMountArgs(linux explicit cache) = %#v, want %#v", got, want)
	}
}

func TestLinuxVirtioFSOwner(t *testing.T) {
	dir := t.TempDir()
	if got := linuxVirtioFSOwner(dir); got.UID != 1000 || got.GID != 1000 {
		t.Fatalf("empty owner = %d:%d, want 1000:1000", got.UID, got.GID)
	}
	if err := vmconfig.SetGuestUser(dir, 1001, 1002); err != nil {
		t.Fatalf("SetGuestUser() error = %v", err)
	}
	if got := linuxVirtioFSOwner(dir); got.UID != 1001 || got.GID != 1002 {
		t.Fatalf("saved owner = %d:%d, want 1001:1002", got.UID, got.GID)
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
