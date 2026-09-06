package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/vmconfig"
)

func TestShellCommandResolveSocketMissingVM(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	err := shellCommand([]string{"no-such-vm-r271"})
	if err == nil {
		t.Fatal("shellCommand(no-such-vm-r271) = nil, want missing-VM error")
	}
	if !strings.Contains(err.Error(), "no running VM") {
		t.Fatalf("shellCommand err = %v, want 'no running VM'", err)
	}
}

func TestShellCommandWindowsQEMUInteractiveMessage(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	name := "qemu-shell"
	dir := filepath.Join(vmconfig.BaseDir(), name+".covevm")
	if err := os.MkdirAll(filepath.Join(dir, "qemu"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "windows.qcow2"), []byte("disk"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "qemu", "metadata.json"), []byte(`{"backend":"qemu-hvf"}`), 0644); err != nil {
		t.Fatal(err)
	}

	err := shellCommand([]string{name})
	if err == nil {
		t.Fatal("shellCommand succeeded")
	}
	if !strings.Contains(err.Error(), "qemu windows shell does not support interactive sessions yet") {
		t.Fatalf("shellCommand error = %v", err)
	}
}

func TestShellCommandWindowsQEMUUsesGlobalVM(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldVMName := vmName
	t.Cleanup(func() { vmName = oldVMName })
	vmName = "qemu-shell-global"
	dir := filepath.Join(vmconfig.BaseDir(), vmName+".covevm")
	if err := os.MkdirAll(filepath.Join(dir, "qemu"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "windows.qcow2"), []byte("disk"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "qemu", "metadata.json"), []byte(`{"backend":"qemu-hvf"}`), 0644); err != nil {
		t.Fatal(err)
	}

	err := shellCommand(nil)
	if err == nil {
		t.Fatal("shellCommand succeeded")
	}
	if !strings.Contains(err.Error(), "qemu windows shell does not support interactive sessions yet") {
		t.Fatalf("shellCommand error = %v", err)
	}
}

// TestShellCommandEarlyBranches covers the flag-parsing branches that exit
// before any socket dial: -h returns nil, an unknown flag surfaces a parse
// error from flag.Parse, and a missing positional VM argument returns the
// "vm name required" error.
func TestShellCommandEarlyBranches(t *testing.T) {
	t.Run("help flag returns nil", func(t *testing.T) {
		for _, alias := range []string{"-h", "--help"} {
			if err := shellCommand([]string{alias}); err != nil {
				t.Fatalf("shellCommand(%q) = %v, want nil", alias, err)
			}
		}
	})

	t.Run("unknown flag returns parse error", func(t *testing.T) {
		err := shellCommand([]string{"-not-a-real-flag"})
		if err == nil {
			t.Fatalf("shellCommand unknown-flag = nil, want parse error")
		}
		if strings.Contains(err.Error(), "vm name required") {
			t.Fatalf("expected parse error, got vm-required: %v", err)
		}
	})

	t.Run("env flag without positional fails vm-required", func(t *testing.T) {
		err := shellCommand([]string{"-env", "FOO=bar"})
		if err == nil || !strings.Contains(err.Error(), "vm name required") {
			t.Fatalf("shellCommand(-env FOO=bar) = %v, want vm name required", err)
		}
	})
}
