package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tmc/cove/internal/vmconfig"
)

func TestSharedFolderMacOSOwnerMountArgs(t *testing.T) {
	tests := []struct {
		name string
		uid  uint32
		gid  uint32
		want []string
	}{
		{"configured", 502, 20, []string{"mount_virtiofs", "-u", "502", "-g", "20", "workspace", "/Volumes/workspace"}},
		{"unset", 0, 0, []string{"mount_virtiofs", "workspace", "/Volumes/workspace"}},
		{"uid only", 502, 0, []string{"mount_virtiofs", "-u", "502", "workspace", "/Volumes/workspace"}},
		{"gid only", 0, 20, []string{"mount_virtiofs", "-g", "20", "workspace", "/Volumes/workspace"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := vmconfig.Save(dir, &vmconfig.Config{GuestUserUID: tt.uid, GuestUserGID: tt.gid}); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "hw.model"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			got := sharedFolderVirtioFSMountArgs(dir, "workspace", "/Volumes/workspace")
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("mount args = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSharedFolderUnreadableConfigDoesNotGuessOwner(t *testing.T) {
	got := sharedFolderVirtioFSMountArgs(t.TempDir(), "workspace", "/Volumes/workspace")
	want := []string{"mount_virtiofs", "workspace", "/Volumes/workspace"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mount args = %v, want %v", got, want)
	}
}

func TestSharedFolderUnknownOSDoesNotMapOwner(t *testing.T) {
	dir := t.TempDir()
	if err := vmconfig.Save(dir, &vmconfig.Config{GuestUserUID: 502, GuestUserGID: 20}); err != nil {
		t.Fatal(err)
	}
	got := sharedFolderVirtioFSMountArgs(dir, "workspace", "/Volumes/workspace")
	want := []string{"mount_virtiofs", "workspace", "/Volumes/workspace"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mount args = %v, want %v", got, want)
	}
}

func TestSharedFolderOwnerMappingPreservesReadOnlyShare(t *testing.T) {
	dir := t.TempDir()
	hostDir := t.TempDir()
	if err := os.Chmod(hostDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := vmconfig.Save(dir, &vmconfig.Config{GuestUserUID: 502, GuestUserGID: 20}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := addSharedFolderEntry(dir, hostDir, "workspace", true); err != nil {
		t.Fatal(err)
	}
	folders := LoadSharedFolders(dir)
	if len(folders) != 1 || !folders[0].ReadOnly {
		t.Fatalf("shared folders = %#v, want one read-only share", folders)
	}
	_ = sharedFolderVirtioFSMountArgs(dir, "workspace", "/Volumes/workspace")
	info, err := os.Stat(hostDir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0700 {
		t.Fatalf("host permissions = %o, want 700", got)
	}
}
