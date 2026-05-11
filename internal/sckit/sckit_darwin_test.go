//go:build darwin

package sckit

import (
	"os"
	"os/exec"
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
				src := filepath.Join(dir, "sw_vers.go")
				if err := os.WriteFile(src, []byte("package main\nimport \"fmt\"\nfunc main() { fmt.Print(\"14.5.1\\n\") }\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				if out, err := exec.Command("go", "build", "-o", filepath.Join(dir, "sw_vers"), src).CombinedOutput(); err != nil {
					t.Fatalf("build sw_vers fake: %v\n%s", err, out)
				}
			}
			t.Setenv("PATH", dir)

			if got := readMacOSVersion(); got != tt.want {
				t.Fatalf("readMacOSVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}
