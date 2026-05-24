package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tmc/cove/internal/vmconfig"
)

func TestParseLogsArgs(t *testing.T) {
	env := commandEnv{Stderr: new(bytes.Buffer)}
	oldVMName := vmName
	t.Cleanup(func() { vmName = oldVMName })
	vmName = ""
	tests := []struct {
		name string
		args []string
		want logsOptions
		fail bool
	}{
		{name: "one-shot", args: []string{"ubuntu"}, want: logsOptions{VM: "ubuntu", Lines: 200}},
		{name: "line limit before vm", args: []string{"-n", "50", "ubuntu"}, want: logsOptions{VM: "ubuntu", Lines: 50}},
		{name: "line limit after vm", args: []string{"ubuntu", "--lines", "75"}, want: logsOptions{VM: "ubuntu", Lines: 75}},
		{name: "follow before vm", args: []string{"-f", "ubuntu"}, want: logsOptions{VM: "ubuntu", Follow: true, Lines: 200}},
		{name: "follow after vm", args: []string{"ubuntu", "-f"}, want: logsOptions{VM: "ubuntu", Follow: true, Lines: 200}},
		{name: "follow long", args: []string{"--follow", "ubuntu"}, want: logsOptions{VM: "ubuntu", Follow: true, Lines: 200}},
		{name: "vm flag", args: []string{"-vm", "ubuntu"}, want: logsOptions{VM: "ubuntu", Lines: 200}},
		{name: "vm flag after follow", args: []string{"-f", "-vm", "ubuntu"}, want: logsOptions{VM: "ubuntu", Follow: true, Lines: 200}},
		{name: "vm flag after positional matching", args: []string{"ubuntu", "-vm", "ubuntu"}, want: logsOptions{VM: "ubuntu", Lines: 200}},
		{name: "missing vm", fail: true},
		{name: "extra arg", args: []string{"ubuntu", "extra"}, fail: true},
		{name: "vm mismatch", args: []string{"ubuntu", "-vm", "other"}, fail: true},
		{name: "bad line limit", args: []string{"ubuntu", "-n", "0"}, fail: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseLogsArgs(env, tt.args)
			if tt.fail {
				if err == nil {
					t.Fatal("parseLogsArgs error = nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseLogsArgs: %v", err)
			}
			if got != tt.want {
				t.Fatalf("parseLogsArgs = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestLogsUsageDocumentsFlagPlacement(t *testing.T) {
	var b strings.Builder
	printLogsUsage(&b)
	for _, want := range []string{"-vm", "-f", "--follow", "-n", "--lines", "before or after the VM name"} {
		if !strings.Contains(b.String(), want) {
			t.Fatalf("usage missing %q:\n%s", want, b.String())
		}
	}
}

func TestParseLogsArgsUsesGlobalVM(t *testing.T) {
	env := commandEnv{Stderr: new(bytes.Buffer)}
	oldVMName := vmName
	t.Cleanup(func() { vmName = oldVMName })
	vmName = "global-vm"
	got, err := parseLogsArgs(env, nil)
	if err != nil {
		t.Fatalf("parseLogsArgs: %v", err)
	}
	if got != (logsOptions{VM: "global-vm", Lines: 200}) {
		t.Fatalf("parseLogsArgs = %#v, want global-vm with default line limit", got)
	}
}

func TestLogsGlobalMissingVMDoesNotCreateDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	oldVMName, oldVMDir := vmName, vmDir
	t.Cleanup(func() {
		vmName, vmDir = oldVMName, oldVMDir
	})
	vmName = "missing-logs-vm"
	vmDir = ""
	err := logsCommand(commandEnv{Stderr: new(bytes.Buffer)}, nil)
	if err == nil {
		t.Fatal("logsCommand succeeded for missing VM")
	}
	if !strings.Contains(err.Error(), `no VM named "missing-logs-vm"`) {
		t.Fatalf("logsCommand error = %q, want no VM named", err)
	}
	if !strings.Contains(err.Error(), "list VMs: cove list") {
		t.Fatalf("logsCommand error = %q, want list hint", err)
	}
	if !strings.Contains(err.Error(), "create a VM: cove up -user <name>") {
		t.Fatalf("logsCommand error = %q, want create hint", err)
	}
	if _, statErr := os.Stat(filepath.Join(vmconfig.BaseDir(), "missing-logs-vm")); !os.IsNotExist(statErr) {
		t.Fatalf("missing logs VM dir stat = %v, want not exist", statErr)
	}
}

func TestLogsGuestCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	base := vmconfig.BaseDir()
	mustTouch(t, filepath.Join(base, "linux", "efi.nvram"))
	mustTouch(t, filepath.Join(base, "linux", "linux-disk.img"))
	mustTouch(t, filepath.Join(base, "mac", "hw.model"))
	mustTouch(t, filepath.Join(base, "mac", "disk.img"))
	mustTouch(t, filepath.Join(base, "mac", "aux.img"))

	tests := []struct {
		name string
		opts logsOptions
		want []string
	}{
		{name: "linux one-shot", opts: logsOptions{VM: "linux", Lines: 200}, want: []string{"journalctl", "--since", "1 hour ago", "-n", "200"}},
		{name: "linux custom lines", opts: logsOptions{VM: "linux", Lines: 50}, want: []string{"journalctl", "--since", "1 hour ago", "-n", "50"}},
		{name: "linux follow", opts: logsOptions{VM: "linux", Follow: true}, want: []string{"journalctl", "-f"}},
		{name: "mac one-shot", opts: logsOptions{VM: "mac", Lines: 200}, want: []string{"/bin/sh", "-lc", "log show --last 1h | tail -n 200"}},
		{name: "mac custom lines", opts: logsOptions{VM: "mac", Lines: 50}, want: []string{"/bin/sh", "-lc", "log show --last 1h | tail -n 50"}},
		{name: "mac follow", opts: logsOptions{VM: "mac", Follow: true}, want: []string{"log", "stream"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := logsGuestCommand(tt.opts)
			if err != nil {
				t.Fatalf("logsGuestCommand: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("logsGuestCommand = %q, want %q", got, tt.want)
			}
		})
	}
}

func mustTouch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0644); err != nil {
		t.Fatal(err)
	}
}
