package main

import (
	"runtime"
	"testing"

	"github.com/tmc/apple/foundation"
	vz "github.com/tmc/apple/virtualization"
)

func TestSharedDirectoryNativePathAndAccess(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	dir := t.TempDir()
	for _, tt := range []struct {
		name     string
		readOnly bool
	}{
		{"read only", true}, {"read write", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			url := foundation.NewURLFileURLWithPath(dir)
			shared := vz.NewSharedDirectoryWithURLReadOnly(url, tt.readOnly)
			if shared.ID == 0 {
				t.Fatal("shared directory is nil")
			}
			if got := shared.URL().Path(); got != dir {
				t.Fatalf("native path = %q, want %q", got, dir)
			}
			if got := shared.IsReadOnly(); got != tt.readOnly {
				t.Fatalf("native readOnly = %v, want %v", got, tt.readOnly)
			}
		})
	}
}
