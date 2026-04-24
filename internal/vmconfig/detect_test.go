package vmconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectOSType(t *testing.T) {
	tests := []struct {
		name string
		file string
		want string
	}{
		{name: "macos", file: "hw.model", want: "macOS"},
		{name: "linux disk", file: "linux-disk.img", want: "Linux"},
		{name: "linux nvram", file: "efi.nvram", want: "Linux"},
		{name: "linux vars", file: "efi-vars.img", want: "Linux"},
		{name: "unknown", want: "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.file != "" {
				if err := os.WriteFile(filepath.Join(dir, tt.file), []byte(tt.file), 0644); err != nil {
					t.Fatalf("WriteFile(%s) error = %v", tt.file, err)
				}
			}
			if got := DetectOSType(dir); got != tt.want {
				t.Fatalf("DetectOSType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHasSuspendState(t *testing.T) {
	dir := t.TempDir()
	if HasSuspendState(dir) {
		t.Fatal("HasSuspendState() = true, want false")
	}
	if err := os.WriteFile(filepath.Join(dir, "suspend.vmstate"), []byte("state"), 0644); err != nil {
		t.Fatalf("WriteFile(suspend.vmstate) error = %v", err)
	}
	if !HasSuspendState(dir) {
		t.Fatal("HasSuspendState() = false, want true")
	}
}
