package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsQEMUDrivePaths(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "vm,with,,commas")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(dir, "windows.iso")
	if err := os.WriteFile(media, nil, 0600); err != nil {
		t.Fatal(err)
	}
	cfg := windowsQEMUConfig{
		CPUCount: 2, MemoryGB: 4, NetworkMode: "none", Headless: true,
		EFICodePath: filepath.Join(dir, "code.fd"),
		EFIVarsPath: filepath.Join(dir, "vars.fd"),
		DiskPath:    filepath.Join(dir, "disk.qcow2"), DiskFormat: "qcow2",
		ISOPath: media, VirtioISOPath: media, AutounattendISOPath: media,
	}
	args, err := windowsQEMUArgs(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var drives []string
	for i, arg := range args {
		if arg == "-drive" {
			drives = append(drives, args[i+1])
		}
	}
	if len(drives) != 6 {
		t.Fatalf("got %d drives, want 6", len(drives))
	}
	for i, path := range []string{cfg.EFICodePath, cfg.EFIVarsPath, cfg.DiskPath, media, media, media} {
		_, value, ok := strings.Cut(drives[i], ",file=")
		if !ok {
			t.Fatalf("drive %q has no file option", drives[i])
		}
		var decoded strings.Builder
		for j := 0; j < len(value); j++ {
			if value[j] == ',' {
				if j+1 == len(value) || value[j+1] != ',' {
					t.Fatalf("drive %q has an unescaped comma in file option", drives[i])
				}
				j++
			}
			decoded.WriteByte(value[j])
		}
		if decoded.String() != path {
			t.Errorf("drive path = %q, want %q", decoded.String(), path)
		}
	}
}
