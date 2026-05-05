package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/vz-macos/internal/vmconfig"
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
	for _, want := range []string{"efi.nvram", "linux-installed", "vmlinuz", "initrd", linuxRootUUIDFileName} {
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

func TestLinuxVirtioFSDeviceConfigsAlwaysIncludesSharedFoldersDevice(t *testing.T) {
	got, err := linuxVirtioFSDeviceConfigs(nil, nil)
	if err != nil {
		t.Fatalf("linuxVirtioFSDeviceConfigs() error = %v", err)
	}
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
	hostDir := t.TempDir()
	got, err := linuxVirtioFSDeviceConfigs([]vmconfig.VolumeMount{{
		HostPath: hostDir,
		Tag:      "work",
	}}, nil)
	if err != nil {
		t.Fatalf("linuxVirtioFSDeviceConfigs() error = %v", err)
	}
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
