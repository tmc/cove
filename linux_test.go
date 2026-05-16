package main

import (
	"os"
	"path/filepath"
	"testing"

	vz "github.com/tmc/apple/virtualization"
)

func TestLoadInstalledLinuxBootArtifacts(t *testing.T) {
	vmDir := t.TempDir()
	if _, ok := loadInstalledLinuxBootArtifacts(vmDir); ok {
		t.Fatal("loadInstalledLinuxBootArtifacts(empty) = ok, want false")
	}

	for name, data := range map[string]string{
		"vmlinuz":             "kernel",
		"initrd":              "initrd",
		linuxRootUUIDFileName: "1234-uuid\n",
	} {
		if err := os.WriteFile(filepath.Join(vmDir, name), []byte(data), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	artifacts, ok := loadInstalledLinuxBootArtifacts(vmDir)
	if !ok {
		t.Fatal("loadInstalledLinuxBootArtifacts(populated) = false, want true")
	}
	if artifacts.rootUUID != "1234-uuid" {
		t.Fatalf("rootUUID = %q, want %q", artifacts.rootUUID, "1234-uuid")
	}
	if got, want := artifacts.commandLine(), "console=tty0 console=hvc0 root=UUID=1234-uuid"; got != want {
		t.Fatalf("commandLine() = %q, want %q", got, want)
	}
	if !hasInstalledLinuxBootArtifacts(vmDir) {
		t.Fatal("hasInstalledLinuxBootArtifacts(populated) = false, want true")
	}
}

func TestCloneOptionalFilesLinuxIncludesBootArtifacts(t *testing.T) {
	files := cloneOptionalFiles("Linux")
	for _, want := range []string{"efi.nvram", "linux-installed", "vmlinuz", "initrd", linuxRootUUIDFileName, linuxRootDeviceFileName} {
		found := false
		for _, got := range files {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("cloneOptionalFiles(Linux) missing %q: %v", want, files)
		}
	}
}

func TestLoadInstalledLinuxBootArtifactsOptionalInitrd(t *testing.T) {
	vmDir := t.TempDir()
	for name, data := range map[string]string{
		"vmlinuz":               "kernel",
		linuxRootUUIDFileName:   "1234-uuid\n",
		linuxRootDeviceFileName: "/dev/vda1\n",
	} {
		if err := os.WriteFile(filepath.Join(vmDir, name), []byte(data), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	artifacts, ok := loadInstalledLinuxBootArtifacts(vmDir)
	if !ok {
		t.Fatal("loadInstalledLinuxBootArtifacts(no initrd) = false, want true")
	}
	if artifacts.initrd != "" {
		t.Fatalf("initrd = %q, want empty", artifacts.initrd)
	}
	if got, want := artifacts.commandLine(), "console=tty0 console=hvc0 root=/dev/vda1 rootfstype=ext4 rw"; got != want {
		t.Fatalf("commandLine() = %q, want %q", got, want)
	}
}

func TestLinuxVirtioFSDeviceConfigsAlwaysIncludesSharedFoldersDevice(t *testing.T) {
	got := linuxVirtioFSDeviceConfigs(nil, nil)
	if len(got) != 1 {
		t.Fatalf("len(linuxVirtioFSDeviceConfigs()) = %d, want 1", len(got))
	}
	if tag := got[0].Tag(); tag != SharedFoldersVirtioFSTag {
		t.Fatalf("shared folders device tag = %q, want %q", tag, SharedFoldersVirtioFSTag)
	}
	if got[0].Share() == nil {
		t.Fatal("shared folders device has nil share, want empty multiple-directory share")
	}
}

func TestLinuxVirtioFSDeviceConfigsPreservesVolumes(t *testing.T) {
	volumeDevice := vz.NewVirtioFileSystemDeviceConfigurationWithTag("work")
	got := linuxVirtioFSDeviceConfigs([]vz.VZVirtioFileSystemDeviceConfiguration{volumeDevice}, nil)
	if len(got) != 2 {
		t.Fatalf("len(linuxVirtioFSDeviceConfigs()) = %d, want 2", len(got))
	}
	if tag := got[0].Tag(); tag != "work" {
		t.Fatalf("volume device tag = %q, want work", tag)
	}
	if tag := got[1].Tag(); tag != SharedFoldersVirtioFSTag {
		t.Fatalf("shared folders device tag = %q, want %q", tag, SharedFoldersVirtioFSTag)
	}
}
