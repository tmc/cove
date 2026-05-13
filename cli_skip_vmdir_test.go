package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

// TestSubcommandSkipsVMDir guards the allowlist of subcommands that must boot
// without creating ~/.vz/vms entries during startup.
func TestSubcommandSkipsVMDir(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want bool
	}{
		{"empty", nil, false},
		{"helper bare", []string{"helper"}, true},
		{"helper daemon", []string{"helper", "daemon"}, true},
		{"helper status", []string{"helper", "status"}, true},
		{"cp uses control socket", []string{"cp"}, true},
		{"ctl uses control socket", []string{"ctl"}, true},
		{"logs is read only", []string{"logs"}, true},
		{"status is read only", []string{"status"}, true},
		{"version", []string{"version"}, true},
		{"vm tree", []string{"vm", "tree"}, true},
		{"vm tree extra args still skips startup VM dir", []string{"vm", "tree", "extra"}, true},
		{"run is not allowlisted", []string{"run"}, false},
		{"run fork-from skips startup VM dir", []string{"run", "-fork-from", "missing:latest"}, true},
		{"run fork-from after other flags skips startup VM dir", []string{"run", "-headless", "-fork-from=missing:latest"}, true},
		{"run after -- does not inspect fork-from", []string{"run", "--", "-fork-from", "missing:latest"}, false},
		{"install is not allowlisted", []string{"install"}, false},
		{"vm is not allowlisted", []string{"vm", "list"}, false},
		{"unknown is not allowlisted", []string{"banana"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := subcommandSkipsVMDir(tt.args); got != tt.want {
				t.Errorf("subcommandSkipsVMDir(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

func TestRerunVMDirForPostCommandSkipsRunForkFrom(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldVMName, oldVMDir := vmName, vmDir
	t.Cleanup(func() {
		vmName, vmDir = oldVMName, oldVMDir
	})
	vmName = "missing-run-fork-child"
	vmDir = ""
	if code := rerunVMDirForPostCommand("run", []string{"-fork-from", "missing:image"}); code != 0 {
		t.Fatalf("rerunVMDirForPostCommand(run -fork-from) = %d, want 0", code)
	}
	if _, err := os.Stat(filepath.Join(vmconfig.BaseDir(), vmName)); !os.IsNotExist(err) {
		t.Fatalf("run fork-from VM dir stat = %v, want not exist", err)
	}
}

func TestRerunVMDirForPostCommandSkipsControlSocketCommands(t *testing.T) {
	for _, tt := range []struct {
		name string
		cmd  string
		args []string
		vm   string
	}{
		{"cp", "cp", []string{"source.txt", "missing-cp-vm:/tmp/source.txt"}, "missing-cp-vm"},
		{"ctl", "ctl", []string{"status"}, "missing-ctl-vm"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			oldVMName, oldVMDir := vmName, vmDir
			t.Cleanup(func() {
				vmName, vmDir = oldVMName, oldVMDir
			})
			vmName = tt.vm
			vmDir = ""
			if code := rerunVMDirForPostCommand(tt.cmd, tt.args); code != 0 {
				t.Fatalf("rerunVMDirForPostCommand(%s) = %d, want 0", tt.cmd, code)
			}
			if _, err := os.Stat(filepath.Join(vmconfig.BaseDir(), tt.vm)); !os.IsNotExist(err) {
				t.Fatalf("%s VM dir stat = %v, want not exist", tt.cmd, err)
			}
		})
	}
}
