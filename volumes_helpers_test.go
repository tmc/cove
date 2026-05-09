package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

func TestTaggedVolumes(t *testing.T) {
	cases := []struct {
		name string
		in   []vmconfig.VolumeMount
		want []vmconfig.VolumeMount
	}{
		{"nil", nil, nil},
		{"empty", []vmconfig.VolumeMount{}, nil},
		{"all untagged",
			[]vmconfig.VolumeMount{{HostPath: "/a"}, {HostPath: "/b"}},
			nil,
		},
		{"all tagged",
			[]vmconfig.VolumeMount{{HostPath: "/a", Tag: "x"}, {HostPath: "/b", Tag: "y"}},
			[]vmconfig.VolumeMount{{HostPath: "/a", Tag: "x"}, {HostPath: "/b", Tag: "y"}},
		},
		{"mixed",
			[]vmconfig.VolumeMount{{HostPath: "/a", Tag: "x"}, {HostPath: "/b"}, {HostPath: "/c", Tag: "z"}},
			[]vmconfig.VolumeMount{{HostPath: "/a", Tag: "x"}, {HostPath: "/c", Tag: "z"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := taggedVolumes(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("taggedVolumes(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestLinuxVirtioFSOwnerDefaults(t *testing.T) {
	dir := t.TempDir()
	got := linuxVirtioFSOwner(dir)
	want := virtioFSOwner{UID: 1000, GID: 1000}
	if got != want {
		t.Errorf("linuxVirtioFSOwner(empty) = %+v, want %+v", got, want)
	}
}

func TestLinuxVirtioFSOwnerOverrideUID(t *testing.T) {
	dir := t.TempDir()
	if err := vmconfig.Save(dir, &vmconfig.Config{GuestUserUID: 1500}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got := linuxVirtioFSOwner(dir)
	want := virtioFSOwner{UID: 1500, GID: 1000}
	if got != want {
		t.Errorf("linuxVirtioFSOwner(uid only) = %+v, want %+v", got, want)
	}
}

func TestLinuxVirtioFSOwnerOverrideBoth(t *testing.T) {
	dir := t.TempDir()
	if err := vmconfig.Save(dir, &vmconfig.Config{GuestUserUID: 2001, GuestUserGID: 2002}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got := linuxVirtioFSOwner(dir)
	want := virtioFSOwner{UID: 2001, GID: 2002}
	if got != want {
		t.Errorf("linuxVirtioFSOwner(both) = %+v, want %+v", got, want)
	}
}

func TestLinuxVirtioFSOwnerMalformedConfig(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("not-json"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := linuxVirtioFSOwner(dir)
	want := virtioFSOwner{UID: 1000, GID: 1000}
	if got != want {
		t.Errorf("linuxVirtioFSOwner(malformed) = %+v, want %+v", got, want)
	}
}
