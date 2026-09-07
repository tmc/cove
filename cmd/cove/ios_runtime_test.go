package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	iosbundle "github.com/tmc/cove/internal/ios/bundle"
	"github.com/tmc/cove/internal/vmconfig"
	"github.com/tmc/cove/internal/vmrun"
)

func TestMacOSRunnerRejectsIOSBeforeMutation(t *testing.T) {
	dir := t.TempDir()
	ios := iosbundle.DefaultConfig()
	if err := vmconfig.Save(dir, &vmconfig.Config{CPU: 8, MemoryGB: 8, IOS: &ios}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	err = runMacOSVMWithConfig(vmrun.RunConfig{OS: vmrun.GuestMacOS}, vmrun.HostConfig{VMDir: dir}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "ios bundle requires") {
		t.Fatalf("run = %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "config.json" {
		t.Fatalf("runner created guest state: %v", entries)
	}
	after, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("runner rewrote ios configuration")
	}
}

func TestIOSCommandUsage(t *testing.T) {
	spec, ok := lookupCommand("ios")
	if !ok {
		t.Fatal("ios command not registered")
	}
	for _, tt := range []struct {
		name string
		args []string
		want int
	}{
		{"missing", nil, 2},
		{"unknown", []string{"unknown"}, 2},
		{"extra", []string{"preflight", "extra"}, 2},
		{"help", []string{"--help"}, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out, stderr bytes.Buffer
			env := commandEnv{Stdout: &out, Stderr: &stderr}
			if got := runRegisteredCommand(env, spec, "ios", tt.args); got != tt.want {
				t.Fatalf("exit = %d, want %d", got, tt.want)
			}
			if !strings.Contains(out.String()+stderr.String(), "usage: cove ios preflight") {
				t.Fatal("missing usage")
			}
		})
	}
}
