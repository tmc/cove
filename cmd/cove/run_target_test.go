package main

import (
	"flag"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/cove/internal/vmconfig"
)

func TestResolveRunTargetExplicitDirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldFlags, oldDir, oldName, oldDisk, oldWindows := flag.CommandLine, vmDir, vmName, diskPath, windowsMode
	t.Cleanup(func() {
		flag.CommandLine, vmDir, vmName, diskPath, windowsMode = oldFlags, oldDir, oldName, oldDisk, oldWindows
	})
	fallback := filepath.Join(vmconfig.BaseDir(), "fallback")
	if err := os.MkdirAll(fallback, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fallback, "windows-disk.img"), []byte("disk"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name                        string
		external, missing, internal bool
		wantErr                     bool
	}{
		{name: "external disk", external: true},
		{name: "internal disk", internal: true},
		{name: "missing external disk", external: true, missing: true, wantErr: true},
		{name: "empty state directory", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			vmDir, vmName, diskPath, windowsMode = dir, "fallback", "", true
			if tt.external {
				diskPath = filepath.Join(t.TempDir(), "target.img")
			}
			if tt.external && !tt.missing {
				if err := os.WriteFile(diskPath, []byte("disk"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tt.internal {
				if err := os.WriteFile(filepath.Join(dir, "windows-disk.img"), []byte("disk"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			flag.CommandLine = flag.NewFlagSet("test", flag.ContinueOnError)
			flag.CommandLine.SetOutput(io.Discard)
			flag.CommandLine.StringVar(&vmDir, "vm-dir", "", "")
			if err := flag.CommandLine.Parse([]string{"-vm-dir", dir}); err != nil {
				t.Fatal(err)
			}
			err := resolveRunTarget()
			if (err != nil) != tt.wantErr {
				t.Errorf("resolveRunTarget() = %v, want error %v", err, tt.wantErr)
			}
			if vmDir != dir {
				t.Errorf("vmDir = %q, want explicit directory %q", vmDir, dir)
			}
		})
	}
}
