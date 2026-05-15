package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

// TestCtlCommandEarlyBranches covers the dispatch paths that exit before
// any control socket is dialed: help args, flag-parse errors, and the
// missing-subcommand usage error.
func TestCtlCommandEarlyBranches(t *testing.T) {
	t.Run("help alias returns nil", func(t *testing.T) {
		for _, alias := range []string{"help", "-h", "--help"} {
			if err := ctlCommand([]string{alias}); err != nil {
				t.Errorf("ctlCommand(%q) = %v, want nil", alias, err)
			}
		}
	})

	t.Run("unknown flag returns parse error", func(t *testing.T) {
		err := ctlCommand([]string{"-not-a-real-ctl-flag"})
		if err == nil {
			t.Fatal("ctlCommand bogus flag: got nil, want parse error")
		}
	})

	t.Run("missing subcommand returns command-required", func(t *testing.T) {
		err := ctlCommand([]string{"-socket", "/tmp/nonexistent.sock"})
		if err == nil || !strings.Contains(err.Error(), "command required") {
			t.Fatalf("ctlCommand no subcmd: got %v, want 'command required'", err)
		}
	})

	t.Run("vnc start explains run flag", func(t *testing.T) {
		err := ctlCommand([]string{"-socket", "/tmp/nonexistent.sock", "vnc", "start"})
		if err == nil {
			t.Fatal("ctlCommand vnc start: got nil, want error")
		}
		for _, want := range []string{"unknown vnc action: start", "use status", "cove run -vnc :5901 -vnc-password <password>"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("ctlCommand vnc start error = %q, want %q", err.Error(), want)
			}
		}
		if strings.Contains(err.Error(), "cove run -vnc :5901)") {
			t.Fatalf("ctlCommand vnc start error still has bare vnc command: %q", err.Error())
		}
	})
}

func TestCtlCommandVMNotFoundBeforeControlSocketHint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	err := ctlCommand([]string{"-vm", "deleted-vm", "status"})
	if err == nil {
		t.Fatal("ctlCommand succeeded for missing VM")
	}
	msg := err.Error()
	for _, want := range []string{`no VM named "deleted-vm"`, vmconfig.BaseDir()} {
		if !strings.Contains(msg, want) {
			t.Fatalf("ctlCommand error = %q, want %q", msg, want)
		}
	}
	for _, notWant := range []string{"control socket not found", "start it with"} {
		if strings.Contains(msg, notWant) {
			t.Fatalf("ctlCommand error = %q, did not want %q", msg, notWant)
		}
	}
}

func TestCtlCommandGlobalVMNotFoundBeforeControlSocketHint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldVMName, oldVMDir := vmName, vmDir
	t.Cleanup(func() {
		vmName, vmDir = oldVMName, oldVMDir
	})
	vmName = "deleted-global-vm"
	vmDir = ""
	err := ctlCommand([]string{"status"})
	if err == nil {
		t.Fatal("ctlCommand succeeded for missing global VM")
	}
	msg := err.Error()
	for _, want := range []string{`no VM named "deleted-global-vm"`, vmconfig.BaseDir()} {
		if !strings.Contains(msg, want) {
			t.Fatalf("ctlCommand error = %q, want %q", msg, want)
		}
	}
	for _, notWant := range []string{"control socket not found", "start it with"} {
		if strings.Contains(msg, notWant) {
			t.Fatalf("ctlCommand error = %q, did not want %q", msg, notWant)
		}
	}
	if _, err := os.Stat(filepath.Join(vmconfig.BaseDir(), vmName)); !os.IsNotExist(err) {
		t.Fatalf("global ctl VM dir stat = %v, want not exist", err)
	}
}

func TestCtlCommandStoppedExistingVMKeepsControlSocketHint(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "cove-ctl-test-")
	if err != nil {
		t.Fatalf("temp home: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("HOME", home)
	vm := "stopped-vm"
	dir := filepath.Join(vmconfig.BaseDir(), vm)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir VM: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "linux-disk.img"), []byte("disk"), 0o644); err != nil {
		t.Fatalf("write disk: %v", err)
	}
	err = ctlCommand([]string{"-vm", vm, "status"})
	if err == nil {
		t.Fatal("ctlCommand succeeded for stopped VM without control socket")
	}
	msg := err.Error()
	for _, want := range []string{"vm is not running: control socket not found", "start it with"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("ctlCommand error = %q, want %q", msg, want)
		}
	}
	if strings.Contains(msg, "no VM named") {
		t.Fatalf("ctlCommand error = %q, did not want not-found diagnostic", msg)
	}
}
