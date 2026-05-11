//go:build darwin

package sckit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadMacOSVersion(t *testing.T) {
	tests := []struct {
		name   string
		script string
		want   string
	}{
		{
			name:   "trims sw_vers output",
			script: "#!/bin/sh\nprintf '14.5.1\\n'\n",
			want:   "14.5.1",
		},
		{
			name: "missing sw_vers",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.script != "" {
				path := filepath.Join(dir, "sw_vers")
				if err := os.WriteFile(path, []byte(tt.script), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", dir)

			if got := readMacOSVersion(); got != tt.want {
				t.Fatalf("readMacOSVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}
