package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseUSBStorageFlag(t *testing.T) {
	dir := t.TempDir()
	disk := filepath.Join(dir, "disk.img")
	if err := os.WriteFile(disk, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		in       string
		wantRO   bool
		wantErr  bool
		wantPath string
	}{
		{"empty", "", false, true, ""},
		{"missing", filepath.Join(dir, "nope.img"), false, true, ""},
		{"plain", disk, false, false, disk},
		{"ro", disk + ":ro", true, false, disk},
		{"readonly", disk + ":readonly", true, false, disk},
		{"rw", disk + ":rw", false, false, disk},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ParseUSBStorageFlag(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if cfg.ReadOnly != tt.wantRO {
				t.Errorf("ReadOnly=%v want %v", cfg.ReadOnly, tt.wantRO)
			}
			if cfg.Path != tt.wantPath {
				t.Errorf("Path=%q want %q", cfg.Path, tt.wantPath)
			}
		})
	}
}

func TestParseUSBStorageFlagMissingWrapsFSErrNotExist(t *testing.T) {
	dir := t.TempDir()
	_, err := ParseUSBStorageFlag(filepath.Join(dir, "nope.img"))
	if err == nil {
		t.Fatal("ParseUSBStorageFlag missing: want error, got nil")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want errors.Is(err, fs.ErrNotExist)", err)
	}
}

func TestUSBStorageSliceString(t *testing.T) {
	var nilS *USBStorageSlice
	if got := nilS.String(); got != "" {
		t.Errorf("nil String=%q want empty", got)
	}
	empty := USBStorageSlice{}
	if got := empty.String(); got != "" {
		t.Errorf("empty String=%q want empty", got)
	}
	s := USBStorageSlice{
		{Path: "/a.img"},
		{Path: "/b.img", ReadOnly: true},
	}
	got := s.String()
	if !strings.Contains(got, "/a.img") || !strings.Contains(got, "/b.img:ro") {
		t.Errorf("String=%q missing entries", got)
	}
	if !strings.Contains(got, ",") {
		t.Errorf("String=%q missing separator", got)
	}
}

func TestUSBStorageSliceSet(t *testing.T) {
	dir := t.TempDir()
	disk := filepath.Join(dir, "d.img")
	if err := os.WriteFile(disk, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var s USBStorageSlice
	if err := s.Set(disk + ":ro"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Set(disk); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if len(s) != 2 {
		t.Fatalf("len=%d want 2", len(s))
	}
	if !s[0].ReadOnly || s[1].ReadOnly {
		t.Errorf("ReadOnly flags wrong: %+v", s)
	}
	if err := s.Set(""); err == nil {
		t.Error("Set(empty) should error")
	}
}
