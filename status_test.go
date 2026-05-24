package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/vmconfig"
)

func TestStatusMissingNamedVMDoesNotCreateDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldVMName, oldVMDir := vmName, vmDir
	t.Cleanup(func() {
		vmName, vmDir = oldVMName, oldVMDir
	})
	vmName = "missing-status-vm"
	vmDir = ""

	err := statusCommand(commandEnv{Stdout: new(bytes.Buffer), Stderr: new(bytes.Buffer)})
	if err == nil {
		t.Fatal("statusCommand succeeded for missing VM")
	}
	msg := err.Error()
	if !strings.Contains(msg, `no VM named "missing-status-vm"`) {
		t.Fatalf("statusCommand error = %q, want no VM named", msg)
	}
	if !strings.Contains(msg, "cove list") || !strings.Contains(msg, "cove up -user <name>") {
		t.Fatalf("statusCommand error = %q, want actionable missing-VM hints", msg)
	}
	if strings.Contains(msg, "control socket not found") || strings.Contains(msg, "start it with") {
		t.Fatalf("statusCommand error = %q, did not want stopped-VM hint", msg)
	}
	if _, statErr := os.Stat(filepath.Join(vmconfig.BaseDir(), "missing-status-vm")); !os.IsNotExist(statErr) {
		t.Fatalf("missing status VM dir stat = %v, want not exist", statErr)
	}
}

func TestStatusVMFlagMissingVMDoesNotCreateDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldVMName, oldVMDir := vmName, vmDir
	t.Cleanup(func() {
		vmName, vmDir = oldVMName, oldVMDir
	})

	for _, tt := range []struct {
		name   string
		global string
		args   []string
	}{
		{name: "global vm before command", global: "missing-global-status-vm"},
		{name: "vm flag after command", args: []string{"-vm", "missing-flag-status-vm"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			vmName = tt.global
			vmDir = ""
			missing := tt.global
			if missing == "" {
				missing = tt.args[1]
			}
			err := statusCommand(commandEnv{Stdout: new(bytes.Buffer), Stderr: new(bytes.Buffer)}, tt.args...)
			if err == nil {
				t.Fatal("statusCommand succeeded for missing VM")
			}
			msg := err.Error()
			if !strings.Contains(msg, `no VM named "`+missing+`"`) {
				t.Fatalf("statusCommand error = %q, want missing VM %q", msg, missing)
			}
			if !strings.Contains(msg, "cove list") || !strings.Contains(msg, "cove up -user <name>") {
				t.Fatalf("statusCommand error = %q, want actionable missing-VM hints", msg)
			}
			if _, statErr := os.Stat(filepath.Join(vmconfig.BaseDir(), missing)); !os.IsNotExist(statErr) {
				t.Fatalf("missing status VM dir stat = %v, want not exist", statErr)
			}
		})
	}
}

func TestParseStatusArgs(t *testing.T) {
	oldVMName := vmName
	t.Cleanup(func() { vmName = oldVMName })
	vmName = "global-vm"
	tests := []struct {
		name string
		args []string
		want statusOptions
		err  string
	}{
		{name: "global default", want: statusOptions{VM: "global-vm"}},
		{name: "positional", args: []string{"pos-vm"}, want: statusOptions{VM: "pos-vm"}},
		{name: "vm flag", args: []string{"-vm", "flag-vm"}, want: statusOptions{VM: "flag-vm"}},
		{name: "matching vm flag and positional", args: []string{"-vm", "same", "same"}, want: statusOptions{VM: "same"}},
		{name: "mismatch", args: []string{"-vm", "flag-vm", "pos-vm"}, err: "does not match"},
		{name: "extra positional", args: []string{"a", "b"}, err: "usage:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseStatusArgs(commandEnv{Stdout: new(bytes.Buffer), Stderr: new(bytes.Buffer)}, tt.args)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("parseStatusArgs error = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseStatusArgs: %v", err)
			}
			if got != tt.want {
				t.Fatalf("parseStatusArgs = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestStatusPostCommandResolutionDoesNotCreateMissingVMDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldVMName, oldVMDir := vmName, vmDir
	t.Cleanup(func() {
		vmName, vmDir = oldVMName, oldVMDir
	})
	vmName = "missing-post-status-vm"
	vmDir = ""

	if code := rerunVMDirForPostCommand("status", nil); code != 0 {
		t.Fatalf("rerunVMDirForPostCommand(status) = %d, want 0", code)
	}
	if _, err := os.Stat(filepath.Join(vmconfig.BaseDir(), "missing-post-status-vm")); !os.IsNotExist(err) {
		t.Fatalf("missing post-status VM dir stat = %v, want not exist", err)
	}
}

func TestStatusStoppedExistingVMReportsStoppedState(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "cove-status-test-")
	if err != nil {
		t.Fatalf("temp home: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("HOME", home)
	oldVMName, oldVMDir := vmName, vmDir
	t.Cleanup(func() {
		vmName, vmDir = oldVMName, oldVMDir
	})
	vmName = "stopped-status-vm"
	vmDir = ""
	dir := filepath.Join(vmconfig.BaseDir(), vmName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir VM: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "linux-disk.img"), []byte("disk"), 0o644); err != nil {
		t.Fatalf("write disk: %v", err)
	}

	err = statusCommand(commandEnv{Stdout: new(bytes.Buffer), Stderr: new(bytes.Buffer)})
	if err == nil {
		t.Fatal("statusCommand succeeded for stopped VM without control socket")
	}
	msg := err.Error()
	for _, want := range []string{`vm "stopped-status-vm" is stopped`, "status requires a running VM", "cove -vm stopped-status-vm run", "cove list"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("statusCommand error = %q, want %q", msg, want)
		}
	}
	if strings.Contains(msg, "control socket not found") {
		t.Fatalf("statusCommand error = %q, did not want raw control socket diagnostic", msg)
	}
	if strings.Contains(msg, "no VM named") {
		t.Fatalf("statusCommand error = %q, did not want not-found diagnostic", msg)
	}
}
