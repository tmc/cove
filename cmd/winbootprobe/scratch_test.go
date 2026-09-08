package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateScratch(t *testing.T) {
	dir := t.TempDir()
	disk := filepath.Join(dir, "disk.img")
	efi := filepath.Join(dir, "efi.img")
	empty := filepath.Join(dir, "empty.img")
	alias := filepath.Join(dir, "alias.img")
	for _, p := range []string{disk, efi, empty} {
		var data []byte
		if p != empty {
			data = []byte("scratch")
		}
		if err := os.WriteFile(p, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Link(disk, alias); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, efi, disk, state, macos string
		wantErr                       bool
	}{
		{name: "legacy"},
		{name: "install", efi: efi, disk: disk, state: dir},
		{name: "cold boot", disk: disk, state: dir},
		{name: "same path", efi: disk, disk: disk, wantErr: true},
		{name: "same inode", efi: alias, disk: disk, wantErr: true},
		{name: "empty target", disk: empty, wantErr: true},
		{name: "directory target", disk: dir, wantErr: true},
		{name: "missing target", disk: filepath.Join(dir, "missing"), wantErr: true},
		{name: "macos target", disk: disk, macos: dir, wantErr: true},
		{name: "macos state", state: dir, macos: dir, wantErr: true},
		{name: "implicit bundle state", state: dir, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := validateScratch(tt.efi, tt.disk, tt.state, tt.macos)
			if (err != nil) != tt.wantErr {
				t.Fatalf("validateScratch() = %v, want error %v", err, tt.wantErr)
			}
		})
	}
}
