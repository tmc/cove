package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreallocateFileRejectsNegativeSize(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "neg")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer f.Close()
	err = preallocateFile(f, -1)
	if err == nil {
		t.Fatal("preallocateFile(-1) returned nil, want error")
	}
	if !strings.Contains(err.Error(), "negative disk size") {
		t.Errorf("error = %q, want contains 'negative disk size'", err)
	}
}

func TestCreateSparseDiskImageBytesExistingIsNoop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "disk.img")
	if err := os.WriteFile(path, []byte("preexisting"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := createSparseDiskImageBytes(path, 16*1024*1024); err != nil {
		t.Fatalf("createSparseDiskImageBytes existing: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "preexisting" {
		t.Fatalf("file contents changed to %q, want preserved", data)
	}
}

func TestParseDiskImageFormat(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    diskImageFormat
		wantErr string
	}{
		{name: "default", in: "", want: diskImageFormatRaw},
		{name: "raw", in: "raw", want: diskImageFormatRaw},
		{name: "asif", in: "ASIF", want: diskImageFormatASIF},
		{name: "bad", in: "qcow2", wantErr: "invalid disk image format"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDiskImageFormat(tt.in)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("parseDiskImageFormat(%q) error = %v, want %q", tt.in, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseDiskImageFormat(%q): %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("parseDiskImageFormat(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
