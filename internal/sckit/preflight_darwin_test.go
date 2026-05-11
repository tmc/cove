//go:build darwin

package sckit

import (
	"path/filepath"
	"testing"
)

func TestLoadCGPreflightMissingFramework(t *testing.T) {
	tests := []struct {
		name string
		path func(string) string
	}{
		{
			name: "missing framework",
			path: func(dir string) string { return filepath.Join(dir, "missing") },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldPath := coreGraphicsFrameworkPath
			oldPreflight := cgPreflightScreenCaptureAccess
			t.Cleanup(func() {
				coreGraphicsFrameworkPath = oldPath
				cgPreflightScreenCaptureAccess = oldPreflight
			})

			coreGraphicsFrameworkPath = tt.path(t.TempDir())
			cgPreflightScreenCaptureAccess = nil
			loadCGPreflight()
			if cgPreflightScreenCaptureAccess != nil {
				t.Fatal("loadCGPreflight registered preflight function for missing framework")
			}
		})
	}
}
