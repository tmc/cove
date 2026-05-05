package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tmc/vz-macos/internal/vmconfig"
)

func TestParseLogsArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want logsOptions
		fail bool
	}{
		{name: "one-shot", args: []string{"ubuntu"}, want: logsOptions{VM: "ubuntu"}},
		{name: "follow before vm", args: []string{"-f", "ubuntu"}, want: logsOptions{VM: "ubuntu", Follow: true}},
		{name: "follow after vm", args: []string{"ubuntu", "-f"}, want: logsOptions{VM: "ubuntu", Follow: true}},
		{name: "follow long", args: []string{"--follow", "ubuntu"}, want: logsOptions{VM: "ubuntu", Follow: true}},
		{name: "missing vm", fail: true},
		{name: "extra arg", args: []string{"ubuntu", "extra"}, fail: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseLogsArgs(tt.args)
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

func TestLogsGuestCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	base := vmconfig.BaseDir()
	mustTouch(t, filepath.Join(base, "linux", "efi.nvram"))
	mustTouch(t, filepath.Join(base, "mac", "hw.model"))

	tests := []struct {
		name string
		opts logsOptions
		want []string
	}{
		{name: "linux one-shot", opts: logsOptions{VM: "linux"}, want: []string{"journalctl", "--since", "1 hour ago"}},
		{name: "linux follow", opts: logsOptions{VM: "linux", Follow: true}, want: []string{"journalctl", "-f"}},
		{name: "mac one-shot", opts: logsOptions{VM: "mac"}, want: []string{"log", "show", "--last", "1h"}},
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
