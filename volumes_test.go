package main

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

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

func TestPrintVolumeMountInfoLinuxPaths(t *testing.T) {
	oldLinux := linuxMode
	linuxMode = true
	defer func() { linuxMode = oldLinux }()

	out := captureStdout(t, func() error {
		printVolumeMountInfo([]vmconfig.VolumeMount{
			{HostPath: "/Users/me/work", Tag: "work"},
			{HostPath: "/Users/me/data"},
		})
		return nil
	})
	for _, want := range []string{
		"guest: mount -t virtiofs work /mnt/work",
		"/Users/me/data -> /mnt/data",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "/Volumes/My Shared Files") {
		t.Fatalf("linux output contains macOS shared path:\n%s", out)
	}
}

func TestPrintVolumeMountInfoMacOSPaths(t *testing.T) {
	oldLinux := linuxMode
	linuxMode = false
	defer func() { linuxMode = oldLinux }()

	out := captureStdout(t, func() error {
		printVolumeMountInfo([]vmconfig.VolumeMount{
			{HostPath: "/Users/me/work", Tag: "work"},
			{HostPath: "/Users/me/data"},
		})
		return nil
	})
	for _, want := range []string{
		"guest: mount_virtiofs work /Volumes/work",
		"/Users/me/data -> /Volumes/My Shared Files",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
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

func TestMountTaggedVolumesOnceSurvivesAgentLoss(t *testing.T) {
	oldLinux := linuxMode
	linuxMode = true
	defer func() { linuxMode = oldLinux }()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	assertDoesNotPanic(t, func() {
		mountTaggedVolumesOnce(ctx, &ControlServer{}, []vmconfig.VolumeMount{
			{Tag: "work"},
		}, defaultLinuxVirtioFSOwner())
	})
}

func TestSetupRosettaInGuestSurvivesAgentLoss(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	assertDoesNotPanic(t, func() {
		setupRosettaInGuest(ctx, &ControlServer{})
	})
}

func TestRosettaRegisterFailureIsBenign(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
		want   bool
	}{
		{
			name:   "register treated as elf path",
			stderr: "rosetta error: failed to open elf at --register\nTrace/breakpoint trap (core dumped)",
			want:   true,
		},
		{
			name:   "mount failure",
			stderr: "mount: /run/rosetta: special device rosetta does not exist",
			want:   false,
		},
		{
			name:   "empty",
			stderr: "",
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rosettaRegisterFailureIsBenign(tt.stderr); got != tt.want {
				t.Fatalf("rosettaRegisterFailureIsBenign(%q) = %v, want %v", tt.stderr, got, tt.want)
			}
		})
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

func TestVolumeSliceString(t *testing.T) {
	tests := []struct {
		name string
		in   volumeSlice
		want string
	}{
		{name: "nil", in: nil, want: ""},
		{name: "empty", in: volumeSlice{}, want: ""},
		{
			name: "host only",
			in:   volumeSlice{{HostPath: "/a"}},
			want: "/a",
		},
		{
			name: "host+tag+ro",
			in:   volumeSlice{{HostPath: "/a", Tag: "work", ReadOnly: true}},
			want: "/a:work:ro",
		},
		{
			name: "host+tag+opts",
			in:   volumeSlice{{HostPath: "/a", Tag: "work", MountOpts: []string{"noatime", "nodev"}}},
			want: "/a:work:noatime,nodev",
		},
		{
			name: "multiple joined",
			in:   volumeSlice{{HostPath: "/a"}, {HostPath: "/b", Tag: "data", ReadOnly: true}},
			want: "/a, /b:data:ro",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.in.String(); got != tt.want {
				t.Fatalf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestVolumeSliceSetAppends(t *testing.T) {
	dir := t.TempDir()
	var vs volumeSlice
	if err := vs.Set(dir); err != nil {
		t.Fatalf("Set(%q) error = %v", dir, err)
	}
	if err := vs.Set(dir + ":tagged:ro"); err != nil {
		t.Fatalf("Set tagged error = %v", err)
	}
	if len(vs) != 2 {
		t.Fatalf("len = %d, want 2", len(vs))
	}
	if vs[0].HostPath == "" || vs[0].Tag != "" || vs[0].ReadOnly {
		t.Fatalf("entry[0] = %#v", vs[0])
	}
	if vs[1].Tag != "tagged" || !vs[1].ReadOnly {
		t.Fatalf("entry[1] = %#v", vs[1])
	}
	if err := vs.Set(""); err == nil {
		t.Fatalf("Set(\"\") error = nil, want error")
	}
}

func assertDoesNotPanic(t *testing.T, fn func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("function panicked: %v", r)
		}
	}()
	fn()
}
